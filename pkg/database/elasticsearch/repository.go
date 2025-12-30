package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// BaseRepository provides common database operations using generics
type BaseRepository[T Document] struct {
	client ElasticClient
	index  string
}

var _ Repository[Document] = (*BaseRepository[Document])(nil)

// NewBaseRepository creates a new base repository
func NewBaseRepository[T Document](client ElasticClient, index string) *BaseRepository[T] {
	return &BaseRepository[T]{
		client: client,
		index:  index,
	}
}

// Index creates or updates a document
func (r *BaseRepository[T]) Index(ctx context.Context, doc *T) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMarshalFailed, err)
	}

	req := esapi.IndexRequest{
		Index:      r.index,
		DocumentID: (*doc).GetID(),
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

// Get retrieves a document by ID
func (r *BaseRepository[T]) Get(ctx context.Context, docID string) (*T, error) {
	req := esapi.GetRequest{
		Index:      r.index,
		DocumentID: docID,
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
func (r *BaseRepository[T]) Delete(ctx context.Context, docID string) error {
	req := esapi.DeleteRequest{
		Index:      r.index,
		DocumentID: docID,
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
func (r *BaseRepository[T]) Search(ctx context.Context, query io.Reader) ([]T, error) {
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
				Source T `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecodeFailed, err)
	}

	results := make([]T, len(response.Hits.Hits))
	for i := range response.Hits.Hits {
		results[i] = response.Hits.Hits[i].Source
	}

	return results, nil
}

// BatchIndex indexes multiple documents using Bulk API
func (r *BaseRepository[T]) BatchIndex(ctx context.Context, docs []*T) error {
	if len(docs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, doc := range docs {
		// Meta line
		meta := []byte(fmt.Sprintf(`{ "index" : { "_index" : "%s", "_id" : "%s" } }%s`, r.index, (*doc).GetID(), "\n"))
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
func (r *BaseRepository[T]) BatchDelete(ctx context.Context, docIDs []string) error {
	if len(docIDs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, id := range docIDs {
		// Meta line only for delete
		meta := []byte(fmt.Sprintf(`{ "delete" : { "_index" : "%s", "_id" : "%s" } }%s`, r.index, id, "\n"))
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
