package elasticsearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/dto"
	"github.com/huynhanx03/go-common/pkg/encoding/json"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

const (
	defaultMaxBatchSize     = 500
	hardMaxBatchSize        = 1000
	defaultMaxDocumentBytes = 2 << 20
	hardMaxDocumentBytes    = 16 << 20
	defaultMaxBulkBytes     = 16 << 20
	hardMaxBulkBytes        = 64 << 20
	maxSearchResponseBytes  = 64 << 20
	maxErrorResponseBytes   = 4 << 10
	maxDocumentIDBytes      = 512
	maxIndexNameBytes       = 255
)

var indexNamePattern = regexp.MustCompile(`^[a-z0-9.][a-z0-9._-]*$`)

// RefreshPolicy controls when an indexed operation becomes searchable. The
// zero value delegates refresh scheduling to Elasticsearch and is the
// production default.
type RefreshPolicy string

const (
	RefreshNone      RefreshPolicy = ""
	RefreshWaitFor   RefreshPolicy = "wait_for"
	RefreshImmediate RefreshPolicy = "true"
)

// RepositoryOption customizes a BaseRepository.
type RepositoryOption func(*RepositoryConfig) error

// RepositoryConfig contains validated constructor option state.
type RepositoryConfig struct {
	allowedFields    map[string]struct{}
	maxBatchSize     int
	maxDocumentBytes int
	maxBulkBytes     int
	refresh          RefreshPolicy
}

// WithAllowedFields restricts client-provided filter and sort keys. With no
// fields, syntax validation remains enabled without a schema whitelist.
func WithAllowedFields(fields ...string) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil {
			return ErrInvalidConfig
		}
		allowed, err := fieldAllowlist(fields)
		if err != nil {
			return err
		}
		config.allowedFields = allowed
		return nil
	}
}

// WithMaxBatchSize configures a per-request bulk operation ceiling.
func WithMaxBatchSize(size int) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil || size <= 0 || size > hardMaxBatchSize {
			return fmt.Errorf("%w: batch size must be between 1 and %d", ErrInvalidConfig, hardMaxBatchSize)
		}
		config.maxBatchSize = size
		return nil
	}
}

// WithMaxDocumentBytes bounds a marshaled source document.
func WithMaxDocumentBytes(size int) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil || size <= 0 || size > hardMaxDocumentBytes {
			return fmt.Errorf("%w: document limit must be between 1 and %d bytes", ErrInvalidConfig, hardMaxDocumentBytes)
		}
		config.maxDocumentBytes = size
		return nil
	}
}

// WithMaxBulkBytes bounds the complete NDJSON bulk request body.
func WithMaxBulkBytes(size int) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil || size <= 0 || size > hardMaxBulkBytes {
			return fmt.Errorf("%w: bulk limit must be between 1 and %d bytes", ErrInvalidConfig, hardMaxBulkBytes)
		}
		config.maxBulkBytes = size
		return nil
	}
}

// WithRefreshPolicy opts into wait_for or an immediate refresh. Immediate
// refresh is useful in tests but should be avoided on high-write production
// paths because it fragments indices and reduces indexing throughput.
func WithRefreshPolicy(policy RefreshPolicy) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil {
			return ErrInvalidConfig
		}
		switch policy {
		case RefreshNone, RefreshWaitFor, RefreshImmediate:
			config.refresh = policy
			return nil
		default:
			return fmt.Errorf("%w: unsupported refresh policy %q", ErrInvalidConfig, policy)
		}
	}
}

// BaseRepository provides bounded, validated Elasticsearch operations.
type BaseRepository[T Model[ID], ID constraints.ID] struct {
	client           ElasticClient
	index            string
	allowedFields    map[string]struct{}
	maxBatchSize     int
	maxDocumentBytes int
	maxBulkBytes     int
	refresh          RefreshPolicy
	initErr          error
}

var _ Repository[*BaseModel[string], string] = (*BaseRepository[*BaseModel[string], string])(nil)

// NewBaseRepository preserves the original constructor. Invalid state is
// retained and returned by every operation.
func NewBaseRepository[T Model[ID], ID constraints.ID](
	client ElasticClient,
	index string,
	options ...RepositoryOption,
) *BaseRepository[T, ID] {
	repository, _ := newBaseRepository[T, ID](client, index, options...)
	return repository
}

// NewCheckedBaseRepository validates dependencies and options during boot.
func NewCheckedBaseRepository[T Model[ID], ID constraints.ID](
	client ElasticClient,
	index string,
	options ...RepositoryOption,
) (*BaseRepository[T, ID], error) {
	repository, err := newBaseRepository[T, ID](client, index, options...)
	if err != nil {
		return nil, err
	}
	return repository, nil
}

func newBaseRepository[T Model[ID], ID constraints.ID](
	client ElasticClient,
	index string,
	options ...RepositoryOption,
) (*BaseRepository[T, ID], error) {
	config := RepositoryConfig{
		maxBatchSize:     defaultMaxBatchSize,
		maxDocumentBytes: defaultMaxDocumentBytes,
		maxBulkBytes:     defaultMaxBulkBytes,
		refresh:          RefreshNone,
	}
	repository := &BaseRepository[T, ID]{client: client, index: index}
	for _, option := range options {
		if option == nil {
			repository.initErr = fmt.Errorf("%w: nil repository option", ErrInvalidConfig)
			return repository, repository.initErr
		}
		if err := option(&config); err != nil {
			repository.initErr = err
			return repository, err
		}
	}
	if isNilElasticClient(client) {
		repository.initErr = fmt.Errorf("%w: nil client", ErrInvalidRepository)
		return repository, repository.initErr
	}
	if err := validateIndexName(index); err != nil {
		repository.initErr = err
		return repository, err
	}
	if config.maxDocumentBytes > config.maxBulkBytes {
		repository.initErr = fmt.Errorf("%w: document limit exceeds bulk limit", ErrInvalidConfig)
		return repository, repository.initErr
	}
	repository.allowedFields = cloneFieldSet(config.allowedFields)
	repository.maxBatchSize = config.maxBatchSize
	repository.maxDocumentBytes = config.maxDocumentBytes
	repository.maxBulkBytes = config.maxBulkBytes
	repository.refresh = config.refresh
	return repository, nil
}

// ValidationError returns a constructor error retained by NewBaseRepository.
func (r *BaseRepository[T, ID]) ValidationError() error {
	if r == nil {
		return ErrInvalidRepository
	}
	return r.initErr
}

// Index creates or replaces a document. Refresh is disabled unless explicitly
// configured with WithRefreshPolicy.
func (r *BaseRepository[T, ID]) Index(ctx context.Context, document *T) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if isNilDocument(document) {
		return fmt.Errorf("%w: nil document", ErrInvalidRepository)
	}
	documentID, err := encodeDocumentID((*document).GetID())
	if err != nil {
		return err
	}
	body, err := r.marshalDocument(document)
	if err != nil {
		return err
	}
	req := esapi.IndexRequest{
		Index:      r.index,
		DocumentID: documentID,
		Body:       bytes.NewReader(body),
		Refresh:    string(r.refresh),
	}
	response, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIndexRequestFailed, err)
	}
	if response == nil || response.Body == nil {
		return fmt.Errorf("%w: empty response", ErrIndexRequestFailed)
	}
	defer closeResponseBody(response.Body)
	if response.IsError() {
		return responseError(ErrIndexRequestFailed, response)
	}
	return nil
}

// Create inserts a document using the repository's historical upsert contract.
func (r *BaseRepository[T, ID]) Create(ctx context.Context, document *T) error {
	return r.Index(ctx, document)
}

// Update replaces a document using the repository's historical upsert contract.
func (r *BaseRepository[T, ID]) Update(ctx context.Context, document *T) error {
	return r.Index(ctx, document)
}

// Get retrieves a document by ID. A missing document returns (nil, nil).
func (r *BaseRepository[T, ID]) Get(ctx context.Context, id ID) (*T, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	documentID, err := encodeDocumentID(id)
	if err != nil {
		return nil, err
	}
	req := esapi.GetRequest{Index: r.index, DocumentID: documentID}
	response, err := req.Do(ctx, r.client)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGetRequestFailed, err)
	}
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("%w: empty response", ErrGetRequestFailed)
	}
	defer closeResponseBody(response.Body)
	if response.StatusCode == 404 {
		return nil, nil
	}
	if response.IsError() {
		return nil, responseError(ErrGetRequestFailed, response)
	}
	body, err := readBounded(response.Body, maxSearchResponseBytes)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Found  *bool  `json:"found"`
		ID     string `json:"_id"`
		Source T      `json:"_source"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeFailed, err)
	}
	if payload.Found != nil && !*payload.Found {
		return nil, nil
	}
	parsedID, err := decodeDocumentID[ID](payload.ID)
	if err != nil {
		return nil, err
	}
	if err := setDocumentID(&payload.Source, parsedID); err != nil {
		return nil, err
	}
	return &payload.Source, nil
}

// Delete removes a document by ID.
func (r *BaseRepository[T, ID]) Delete(ctx context.Context, id ID) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	documentID, err := encodeDocumentID(id)
	if err != nil {
		return err
	}
	req := esapi.DeleteRequest{Index: r.index, DocumentID: documentID}
	response, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteRequestFailed, err)
	}
	if response == nil || response.Body == nil {
		return fmt.Errorf("%w: empty response", ErrDeleteRequestFailed)
	}
	defer closeResponseBody(response.Body)
	if response.IsError() {
		return responseError(ErrDeleteRequestFailed, response)
	}
	return nil
}

// Search executes a trusted raw query. Client-derived QueryOptions should use
// Find so field, pagination, and cursor validation cannot be bypassed.
func (r *BaseRepository[T, ID]) Search(ctx context.Context, query io.Reader) ([]*T, error) {
	page, err := r.searchPage(ctx, query)
	if err != nil {
		return nil, err
	}
	return page.documents, nil
}

// Find retrieves one bounded page using immutable validated query options.
func (r *BaseRepository[T, ID]) Find(
	ctx context.Context,
	options *dto.QueryOptions,
) (*dto.Paginated[*T], error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	fields := sortedFields(r.allowedFields)
	query, err := BuildSearchQueryChecked(options, fields...)
	if err != nil {
		return nil, err
	}
	pagination, err := normalizePagination(queryPagination(options))
	if err != nil {
		return nil, err
	}
	hasCursor := cursorProvided(pagination.Cursor)
	if hasCursor {
		query["size"] = pagination.PageSize + 1
	}
	queryBody, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMarshalFailed, err)
	}
	page, err := r.searchPage(ctx, bytes.NewReader(queryBody))
	if err != nil {
		return nil, err
	}
	if !page.totalExact {
		return nil, fmt.Errorf("%w: total hits are not exact", ErrSearchRequestFailed)
	}

	metadata, err := dto.CalculatePaginationChecked(
		pagination.Page,
		pagination.PageSize,
		page.total,
	)
	if err != nil {
		return nil, err
	}
	documents := page.documents
	if hasCursor {
		hasNext := len(documents) > pagination.PageSize
		if hasNext {
			documents = documents[:pagination.PageSize]
			page.sortValues = page.sortValues[:pagination.PageSize]
		}
		metadata.HasPrev = true
		metadata.HasNext = hasNext
	}
	if metadata.HasNext && len(page.sortValues) >= len(documents) && len(documents) > 0 {
		cursor := page.sortValues[len(documents)-1]
		if len(cursor) > 0 {
			metadata.NextCursor = append([]any(nil), cursor...)
		}
	}
	return &dto.Paginated[*T]{Records: &documents, Pagination: metadata}, nil
}

// CreateBulk indexes a bounded document batch and inspects per-item failures.
func (r *BaseRepository[T, ID]) CreateBulk(ctx context.Context, documents []*T) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if len(documents) == 0 {
		return nil
	}
	if len(documents) > r.maxBatchSize {
		return fmt.Errorf("%w: got %d, limit is %d", ErrBatchTooLarge, len(documents), r.maxBatchSize)
	}
	body, err := r.buildCreateBulkBody(documents)
	if err != nil {
		return err
	}
	return r.executeBulk(ctx, body, ErrIndexRequestFailed)
}

// DeleteBulk deletes a bounded document ID batch and inspects item failures.
func (r *BaseRepository[T, ID]) DeleteBulk(ctx context.Context, ids []ID) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > r.maxBatchSize {
		return fmt.Errorf("%w: got %d, limit is %d", ErrBatchTooLarge, len(ids), r.maxBatchSize)
	}
	var buffer bytes.Buffer
	for index, id := range ids {
		documentID, err := encodeDocumentID(id)
		if err != nil {
			return fmt.Errorf("delete item %d: %w", index, err)
		}
		metadata := map[string]any{
			"delete": map[string]any{"_index": r.index, "_id": documentID},
		}
		if err := appendNDJSON(&buffer, metadata, r.maxBulkBytes); err != nil {
			return err
		}
	}
	return r.executeBulk(ctx, buffer.Bytes(), ErrDeleteRequestFailed)
}

// Exists checks whether a document exists by ID.
func (r *BaseRepository[T, ID]) Exists(ctx context.Context, id ID) (bool, error) {
	if err := r.ready(ctx); err != nil {
		return false, err
	}
	documentID, err := encodeDocumentID(id)
	if err != nil {
		return false, err
	}
	req := esapi.ExistsRequest{Index: r.index, DocumentID: documentID}
	response, err := req.Do(ctx, r.client)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrGetRequestFailed, err)
	}
	if response == nil || response.Body == nil {
		return false, fmt.Errorf("%w: empty response", ErrGetRequestFailed)
	}
	defer closeResponseBody(response.Body)
	switch response.StatusCode {
	case 200:
		return true, nil
	case 404:
		return false, nil
	default:
		return false, responseError(ErrGetRequestFailed, response)
	}
}

type searchPageResult[T any] struct {
	documents  []*T
	sortValues [][]any
	total      int64
	totalExact bool
}

type searchResponse[T any] struct {
	TimedOut bool `json:"timed_out"`
	Shards   struct {
		Failed int `json:"failed"`
	} `json:"_shards"`
	Hits struct {
		Total json.RawMessage `json:"total"`
		Hits  []struct {
			ID     string `json:"_id"`
			Source T      `json:"_source"`
			Sort   []any  `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
}

func (r *BaseRepository[T, ID]) searchPage(
	ctx context.Context,
	query io.Reader,
) (*searchPageResult[T], error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	if query == nil || isNilReader(query) {
		return nil, fmt.Errorf("%w: nil search body", ErrInvalidQuery)
	}
	req := esapi.SearchRequest{Index: []string{r.index}, Body: query}
	response, err := req.Do(ctx, r.client)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSearchRequestFailed, err)
	}
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("%w: empty response", ErrSearchRequestFailed)
	}
	defer closeResponseBody(response.Body)
	if response.IsError() {
		return nil, responseError(ErrSearchRequestFailed, response)
	}
	body, err := readBounded(response.Body, maxSearchResponseBytes)
	if err != nil {
		return nil, err
	}
	var payload searchResponse[T]
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeFailed, err)
	}
	if payload.TimedOut {
		return nil, fmt.Errorf("%w: search timed out", ErrSearchRequestFailed)
	}
	if payload.Shards.Failed > 0 {
		return nil, fmt.Errorf("%w: %d shards failed", ErrSearchRequestFailed, payload.Shards.Failed)
	}
	total, exact, err := decodeTotalHits(payload.Hits.Total)
	if err != nil {
		return nil, err
	}
	result := &searchPageResult[T]{
		documents:  make([]*T, len(payload.Hits.Hits)),
		sortValues: make([][]any, len(payload.Hits.Hits)),
		total:      total,
		totalExact: exact,
	}
	for index := range payload.Hits.Hits {
		hit := &payload.Hits.Hits[index]
		id, err := decodeDocumentID[ID](hit.ID)
		if err != nil {
			return nil, fmt.Errorf("search hit %d: %w", index, err)
		}
		if err := setDocumentID(&hit.Source, id); err != nil {
			return nil, fmt.Errorf("search hit %d: %w", index, err)
		}
		result.documents[index] = &hit.Source
		result.sortValues[index] = append([]any(nil), hit.Sort...)
	}
	return result, nil
}

func (r *BaseRepository[T, ID]) buildCreateBulkBody(documents []*T) ([]byte, error) {
	var buffer bytes.Buffer
	for index, document := range documents {
		if isNilDocument(document) {
			return nil, fmt.Errorf("%w: nil document at index %d", ErrInvalidRepository, index)
		}
		documentID, err := encodeDocumentID((*document).GetID())
		if err != nil {
			return nil, fmt.Errorf("index item %d: %w", index, err)
		}
		metadata := map[string]any{
			"index": map[string]any{"_index": r.index, "_id": documentID},
		}
		if err := appendNDJSON(&buffer, metadata, r.maxBulkBytes); err != nil {
			return nil, err
		}
		source, err := r.marshalDocument(document)
		if err != nil {
			return nil, fmt.Errorf("index item %d: %w", index, err)
		}
		if err := appendRawNDJSON(&buffer, source, r.maxBulkBytes); err != nil {
			return nil, err
		}
	}
	return buffer.Bytes(), nil
}

func (r *BaseRepository[T, ID]) executeBulk(
	ctx context.Context,
	body []byte,
	requestError error,
) error {
	req := esapi.BulkRequest{Body: bytes.NewReader(body), Refresh: string(r.refresh)}
	response, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("%w: %w", requestError, err)
	}
	if response == nil || response.Body == nil {
		return fmt.Errorf("%w: empty response", requestError)
	}
	defer closeResponseBody(response.Body)
	if response.IsError() {
		return responseError(requestError, response)
	}
	responseBody, err := readBounded(response.Body, maxSearchResponseBytes)
	if err != nil {
		return err
	}
	var payload struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
			Error  *struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return fmt.Errorf("%w: %w", ErrDecodeFailed, err)
	}
	if !payload.Errors {
		return nil
	}
	failures := 0
	firstReason := "unspecified item failure"
	for _, item := range payload.Items {
		for _, operation := range item {
			if operation.Status < 200 || operation.Status >= 300 || operation.Error != nil {
				failures++
				if failures == 1 && operation.Error != nil {
					firstReason = strings.TrimSpace(operation.Error.Type + ": " + operation.Error.Reason)
				}
			}
		}
	}
	return fmt.Errorf(
		"%w: %d of %d operations failed: %s",
		ErrBulkPartialFailure,
		failures,
		len(payload.Items),
		firstReason,
	)
}

func (r *BaseRepository[T, ID]) marshalDocument(document *T) ([]byte, error) {
	body, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMarshalFailed, err)
	}
	if len(body) == 0 || len(body) > r.maxDocumentBytes {
		return nil, fmt.Errorf("%w: got %d bytes, limit is %d", ErrDocumentTooLarge, len(body), r.maxDocumentBytes)
	}
	return body, nil
}

func (r *BaseRepository[T, ID]) ready(ctx context.Context) error {
	if r == nil {
		return ErrInvalidRepository
	}
	if r.initErr != nil {
		return r.initErr
	}
	if isNilElasticClient(r.client) || r.index == "" {
		return ErrInvalidRepository
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func validateIndexName(index string) error {
	if index == "" || len(index) > maxIndexNameBytes || index == "." || index == ".." || !indexNamePattern.MatchString(index) {
		return fmt.Errorf("%w: index %q", ErrInvalidIdentifier, index)
	}
	return nil
}

func encodeDocumentID[ID constraints.ID](id ID) (string, error) {
	value := reflect.ValueOf(id)
	switch value.Kind() {
	case reflect.String:
		result := value.String()
		if strings.TrimSpace(result) == "" || len(result) > maxDocumentIDBytes {
			return "", ErrInvalidID
		}
		return result, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value.Int() <= 0 {
			return "", ErrInvalidID
		}
		return strconv.FormatInt(value.Int(), 10), nil
	default:
		return "", ErrInvalidID
	}
}

func decodeDocumentID[ID constraints.ID](encoded string) (ID, error) {
	var result ID
	if strings.TrimSpace(encoded) == "" || len(encoded) > maxDocumentIDBytes {
		return result, ErrInvalidID
	}
	value := reflect.ValueOf(&result).Elem()
	switch value.Kind() {
	case reflect.String:
		value.SetString(encoded)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(encoded, 10, value.Type().Bits())
		if err != nil || parsed <= 0 {
			return result, ErrInvalidID
		}
		value.SetInt(parsed)
	default:
		return result, ErrInvalidID
	}
	return result, nil
}

func setDocumentID[T Model[ID], ID constraints.ID](document *T, id ID) (err error) {
	if isNilDocument(document) {
		return fmt.Errorf("%w: nil decoded document", ErrDecodeFailed)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: model SetID panicked: %v", ErrDecodeFailed, recovered)
		}
	}()
	(*document).SetID(id)
	return nil
}

func isNilDocument[T any](document *T) bool {
	if document == nil {
		return true
	}
	value := reflect.ValueOf(*document)
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func isNilElasticClient(client ElasticClient) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func isNilReader(reader io.Reader) bool {
	value := reflect.ValueOf(reader)
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func cloneFieldSet(fields map[string]struct{}) map[string]struct{} {
	if fields == nil {
		return nil
	}
	result := make(map[string]struct{}, len(fields))
	for field := range fields {
		result[field] = struct{}{}
	}
	return result
}

func sortedFields(fields map[string]struct{}) []string {
	if len(fields) == 0 {
		return nil
	}
	result := make([]string, 0, len(fields))
	for field := range fields {
		result = append(result, field)
	}
	sort.Strings(result)
	return result
}

func appendNDJSON(buffer *bytes.Buffer, value any, maximum int) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMarshalFailed, err)
	}
	return appendRawNDJSON(buffer, encoded, maximum)
}

func appendRawNDJSON(buffer *bytes.Buffer, encoded []byte, maximum int) error {
	if buffer == nil || maximum <= 0 {
		return ErrInvalidConfig
	}
	if len(encoded) > maximum-buffer.Len()-1 {
		return fmt.Errorf("%w: bulk body exceeds %d bytes", ErrDocumentTooLarge, maximum)
	}
	_, _ = buffer.Write(encoded)
	_ = buffer.WriteByte('\n')
	return nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	if reader == nil || maximum <= 0 {
		return nil, ErrInvalidConfig
	}
	body, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeFailed, err)
	}
	if int64(len(body)) > maximum {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrResponseTooLarge, maximum)
	}
	return body, nil
}

func responseError(base error, response *esapi.Response) error {
	if response == nil {
		return fmt.Errorf("%w: empty response", base)
	}
	body, readErr := readBounded(response.Body, maxErrorResponseBytes)
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = "no response body"
	}
	result := fmt.Errorf("%w: status=%s body=%q", base, response.Status(), detail)
	if readErr != nil && !errors.Is(readErr, ErrResponseTooLarge) {
		return errors.Join(result, readErr)
	}
	return result
}

func closeResponseBody(body io.ReadCloser) {
	if body != nil {
		_ = body.Close()
	}
}

func decodeTotalHits(raw json.RawMessage) (int64, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, true, nil
	}
	var numeric int64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		if numeric < 0 {
			return 0, false, fmt.Errorf("%w: negative hit total", ErrDecodeFailed)
		}
		return numeric, true, nil
	}
	var object struct {
		Value    int64  `json:"value"`
		Relation string `json:"relation"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.Value < 0 {
		if err != nil {
			return 0, false, fmt.Errorf("%w: %w", ErrDecodeFailed, err)
		}
		return 0, false, fmt.Errorf("%w: negative hit total", ErrDecodeFailed)
	}
	return object.Value, object.Relation == "" || object.Relation == "eq", nil
}
