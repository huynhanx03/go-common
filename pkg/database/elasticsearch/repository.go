package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/dto"
)

// BaseRepository provides common database operations using generics
type BaseRepository[T Model[ID], ID constraints.ID] struct {
	client ElasticClient
	index  string
}

// NewBaseRepository creates a new base repository
func NewBaseRepository[T Model[ID], ID constraints.ID](client ElasticClient, index string) *BaseRepository[T, ID] {
	var _ Repository[T, ID] = (*BaseRepository[T, ID])(nil)

	return &BaseRepository[T, ID]{
		client: client,
		index:  index,
	}
}

// Index creates or updates a document
func (r *BaseRepository[T, ID]) Index(ctx context.Context, doc *T) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMarshalFailed, err)
	}

	req := esapi.IndexRequest{
		Index:      r.index,
		DocumentID: fmt.Sprintf("%v", (*doc).GetID()),
		Body:       bytes.NewReader(body),
		Refresh:    "true", // Force refresh for immediate consistency (optional, good for dev)
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIndexRequestFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("%w: %s", ErrIndexRequestFailed, res.Status())
	}

	return nil
}

// Create inserts a new document
func (r *BaseRepository[T, ID]) Create(ctx context.Context, doc *T) error {
	return r.Index(ctx, doc)
}

// Update updates a document by ID
func (r *BaseRepository[T, ID]) Update(ctx context.Context, doc *T) error {
	return r.Index(ctx, doc)
}

// Get retrieves a document by ID
func (r *BaseRepository[T, ID]) Get(ctx context.Context, docID ID) (*T, error) {
	req := esapi.GetRequest{
		Index:      r.index,
		DocumentID: fmt.Sprintf("%v", docID),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGetRequestFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		if res.StatusCode == 404 {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("%w: %s", ErrGetRequestFailed, res.Status())
	}

	var response struct {
		Source T `json:"_source"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecodeFailed, err)
	}

	return &response.Source, nil
}

// Delete removes a document by ID
func (r *BaseRepository[T, ID]) Delete(ctx context.Context, docID ID) error {
	req := esapi.DeleteRequest{
		Index:      r.index,
		DocumentID: fmt.Sprintf("%v", docID),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDeleteRequestFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("%w: %s", ErrDeleteRequestFailed, res.Status())
	}

	return nil
}

// Search executes a raw query
func (r *BaseRepository[T, ID]) Search(ctx context.Context, query io.Reader) ([]*T, error) {
	req := esapi.SearchRequest{
		Index: []string{r.index},
		Body:  query,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSearchRequestFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("%w: %s", ErrSearchRequestFailed, res.Status())
	}

	var response struct {
		Hits struct {
			Hits []struct {
				ID     ID `json:"_id"`
				Source T  `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecodeFailed, err)
	}

	results := make([]*T, len(response.Hits.Hits))
	for i := range response.Hits.Hits {
		item := response.Hits.Hits[i].Source
		item.SetID(response.Hits.Hits[i].ID)
		results[i] = &item
	}

	return results, nil
}

// Find retrieves documents with pagination, search/filter, and sorting
func (r *BaseRepository[T, ID]) Find(ctx context.Context, opts *dto.QueryOptions) (*dto.Paginated[*T], error) {
	if opts == nil {
		opts = &dto.QueryOptions{}
	}
	if opts.Pagination == nil {
		opts.Pagination = &dto.PaginationOptions{}
	}
	opts.Pagination.SetDefaults()

	// Build generic query
	queryMap := BuildSearchQuery(opts)

	// Marshal to JSON
	queryJSON, err := json.Marshal(queryMap)
	if err != nil {
		return nil, err
	}

	// Execute search
	docs, err := r.Search(ctx, bytes.NewReader(queryJSON))
	if err != nil {
		return nil, err
	}

	// Calculate pagination info (simple length based for now as per previous logic)
	pagination := dto.CalculatePagination(opts.Pagination.Page, opts.Pagination.PageSize, int64(len(docs)))

	return &dto.Paginated[*T]{
		Records:    &docs,
		Pagination: pagination,
	}, nil
}

// BatchCreate inserts multiple documents using Bulk API
func (r *BaseRepository[T, ID]) BatchCreate(ctx context.Context, docs []*T) error {
	if len(docs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, doc := range docs {
		// Meta line
		meta := []byte(fmt.Sprintf(`{ "index" : { "_index" : "%s", "_id" : "%v" } }%s`, r.index, (*doc).GetID(), "\n"))
		buf.Write(meta)

		// Data line
		data, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("failed to marshal doc: %w", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	res, err := r.client.Bulk(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIndexRequestFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("%w: %s", ErrIndexRequestFailed, res.Status())
	}

	return nil
}

// BatchDelete deletes multiple documents using Bulk API
func (r *BaseRepository[T, ID]) BatchDelete(ctx context.Context, docIDs []ID) error {
	if len(docIDs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, id := range docIDs {
		// Meta line only for delete
		meta := []byte(fmt.Sprintf(`{ "delete" : { "_index" : "%s", "_id" : "%v" } }%s`, r.index, id, "\n"))
		buf.Write(meta)
	}

	res, err := r.client.Bulk(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDeleteRequestFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("%w: %s", ErrDeleteRequestFailed, res.Status())
	}

	return nil
}

// Exists checks whether a document exists by its ID
func (r *BaseRepository[T, ID]) Exists(ctx context.Context, id ID) (bool, error) {
	req := esapi.ExistsRequest{
		Index:      r.index,
		DocumentID: fmt.Sprintf("%v", id),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrGetRequestFailed, err)
	}
	defer res.Body.Close()

	if res.StatusCode == 200 {
		return true, nil
	}
	if res.StatusCode == 404 {
		return false, nil
	}
	return false, fmt.Errorf("%w: %s", ErrGetRequestFailed, res.Status())
}
