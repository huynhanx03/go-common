package mongodb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/huynhanx03/go-common/pkg/dto"
)

const (
	defaultRepositoryTimeout = 30 * time.Second
	defaultMaxBatchSize      = 500
	hardMaxBatchSize         = 1000
)

// RepositoryOption customizes a MongoDB BaseRepository.
type RepositoryOption func(*RepositoryConfig) error

// RepositoryConfig contains validated constructor option state.
type RepositoryConfig struct {
	allowedFields map[string]struct{}
	maxBatchSize  int
	timeout       time.Duration
}

// WithAllowedFields restricts client-provided filter and sort keys. An empty
// option list keeps syntax validation but does not apply a schema whitelist.
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

// WithMaxBatchSize configures the bulk safety limit up to the hard ceiling.
func WithMaxBatchSize(size int) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil || size <= 0 || size > hardMaxBatchSize {
			return fmt.Errorf("%w: batch size must be between 1 and %d", ErrInvalidConfig, hardMaxBatchSize)
		}
		config.maxBatchSize = size
		return nil
	}
}

// WithRepositoryTimeout configures the duration returned by GetContext.
func WithRepositoryTimeout(timeout time.Duration) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil || timeout <= 0 || timeout > 10*time.Minute {
			return fmt.Errorf("%w: repository timeout must be between 1ns and 10m", ErrInvalidConfig)
		}
		config.timeout = timeout
		return nil
	}
}

// BaseRepository provides bounded, validated common MongoDB operations.
type BaseRepository[T Document] struct {
	collection    *mongo.Collection
	timeout       time.Duration
	allowedFields map[string]struct{}
	maxBatchSize  int
	initErr       error
}

var _ Repository[*BaseModel] = (*BaseRepository[*BaseModel])(nil)

// NewBaseRepository preserves the original constructor. Invalid state is
// retained and returned by every database operation.
func NewBaseRepository[T Document](
	collection *mongo.Collection,
	options ...RepositoryOption,
) *BaseRepository[T] {
	repository, _ := newBaseRepository[T](collection, options...)
	return repository
}

// NewCheckedBaseRepository validates repository dependencies during boot.
func NewCheckedBaseRepository[T Document](
	collection *mongo.Collection,
	options ...RepositoryOption,
) (*BaseRepository[T], error) {
	repository, err := newBaseRepository[T](collection, options...)
	if err != nil {
		return nil, err
	}
	return repository, nil
}

func newBaseRepository[T Document](
	collection *mongo.Collection,
	options ...RepositoryOption,
) (*BaseRepository[T], error) {
	config := RepositoryConfig{
		maxBatchSize: defaultMaxBatchSize,
		timeout:      defaultRepositoryTimeout,
	}
	repository := &BaseRepository[T]{collection: collection}
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
	if collection == nil {
		repository.initErr = fmt.Errorf("%w: nil collection", ErrInvalidRepository)
		return repository, repository.initErr
	}
	repository.timeout = config.timeout
	repository.maxBatchSize = config.maxBatchSize
	repository.allowedFields = cloneFieldSet(config.allowedFields)
	return repository, nil
}

// ValidationError returns a constructor error retained by NewBaseRepository.
func (r *BaseRepository[T]) ValidationError() error {
	if r == nil {
		return ErrInvalidRepository
	}
	return r.initErr
}

// GetContext creates a context with the repository timeout.
func (r *BaseRepository[T]) GetContext() (context.Context, context.CancelFunc) {
	if r == nil || r.timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), r.timeout)
}

// GetCollection returns the underlying collection, or nil for an invalid
// repository. Callers must not replace or close its owning client.
func (r *BaseRepository[T]) GetCollection() *mongo.Collection {
	if r == nil {
		return nil
	}
	return r.collection
}

// Get retrieves a document by ID.
func (r *BaseRepository[T]) Get(ctx context.Context, id primitive.ObjectID) (*T, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	if id.IsZero() {
		return nil, ErrInvalidID
	}
	var model T
	if err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&model); err != nil {
		return nil, fmt.Errorf("mongodb: get document: %w", err)
	}
	return &model, nil
}

// Create inserts a new document.
func (r *BaseRepository[T]) Create(ctx context.Context, model *T) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if isNilDocument(model) {
		return fmt.Errorf("%w: nil document", ErrInvalidRepository)
	}
	result, err := r.collection.InsertOne(ctx, model)
	if err != nil {
		return fmt.Errorf("mongodb: create document: %w", err)
	}
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		(*model).SetID(objectID)
	}
	return nil
}

// Update updates a document by ID.
func (r *BaseRepository[T]) Update(ctx context.Context, model *T) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if isNilDocument(model) {
		return fmt.Errorf("%w: nil document", ErrInvalidRepository)
	}
	id := (*model).GetID()
	if id.IsZero() {
		return ErrInvalidID
	}
	(*model).UpdateTimestamp()
	update, err := encodeUpdateDocument(model)
	if err != nil {
		return err
	}
	result, err := r.collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": update})
	if err != nil {
		return fmt.Errorf("mongodb: update document: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("mongodb: update document: %w", mongo.ErrNoDocuments)
	}
	return nil
}

// Delete removes a document by ID.
func (r *BaseRepository[T]) Delete(ctx context.Context, id primitive.ObjectID) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if id.IsZero() {
		return ErrInvalidID
	}
	result, err := r.collection.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return fmt.Errorf("mongodb: delete document: %w", err)
	}
	if result.DeletedCount == 0 {
		return fmt.Errorf("mongodb: delete document: %w", mongo.ErrNoDocuments)
	}
	return nil
}

// Exists checks whether a document exists by its ID.
func (r *BaseRepository[T]) Exists(ctx context.Context, id primitive.ObjectID) (bool, error) {
	if err := r.ready(ctx); err != nil {
		return false, err
	}
	if id.IsZero() {
		return false, ErrInvalidID
	}
	var projection struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	err := r.collection.FindOne(
		ctx,
		bson.M{"_id": id},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Decode(&projection)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mongodb: check document existence: %w", err)
	}
	return !projection.ID.IsZero(), nil
}

// Find retrieves one bounded page with immutable validated query options.
func (r *BaseRepository[T]) Find(
	ctx context.Context,
	opts *dto.QueryOptions,
) (*dto.Paginated[*T], error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	fields := sortedFields(r.allowedFields)
	filter, findOptions, err := ApplyQueryOptionsChecked(opts, fields...)
	if err != nil {
		return nil, err
	}
	baseFilter, err := BuildFilterChecked(queryFilters(opts), fields...)
	if err != nil {
		return nil, err
	}
	paginationOptions, err := normalizePagination(queryPagination(opts))
	if err != nil {
		return nil, err
	}

	totalItems, err := r.collection.CountDocuments(ctx, baseFilter)
	if err != nil {
		return nil, fmt.Errorf("mongodb: count documents: %w", err)
	}
	hasCursor := cursorProvided(paginationOptions.Cursor)
	if hasCursor {
		findOptions.SetLimit(int64(paginationOptions.PageSize + 1))
	}
	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("mongodb: find documents: %w", err)
	}
	records := make([]*T, 0, paginationOptions.PageSize)
	for cursor.Next(ctx) {
		var model T
		if err := cursor.Decode(&model); err != nil {
			return nil, joinCursorError(
				fmt.Errorf("mongodb: decode document: %w", err),
				cursor.Close(ctx),
			)
		}
		records = append(records, &model)
	}
	if err := cursor.Err(); err != nil {
		return nil, joinCursorError(
			fmt.Errorf("mongodb: iterate documents: %w", err),
			cursor.Close(ctx),
		)
	}
	if err := cursor.Close(ctx); err != nil {
		return nil, fmt.Errorf("mongodb: close cursor: %w", err)
	}

	metadata, err := dto.CalculatePaginationChecked(
		paginationOptions.Page,
		paginationOptions.PageSize,
		totalItems,
	)
	if err != nil {
		return nil, err
	}
	if hasCursor {
		hasNext := len(records) > paginationOptions.PageSize
		if hasNext {
			records = records[:paginationOptions.PageSize]
		}
		metadata.HasPrev = true
		metadata.HasNext = hasNext
	}
	if len(records) > 0 && metadata.HasNext {
		lastID := (*records[len(records)-1]).GetID()
		if !lastID.IsZero() {
			metadata.NextCursor = lastID.Hex()
		}
	}

	return &dto.Paginated[*T]{
		Records:    &records,
		Pagination: metadata,
	}, nil
}

// CreateBulk inserts a bounded list of documents.
func (r *BaseRepository[T]) CreateBulk(ctx context.Context, models []*T) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if len(models) == 0 {
		return nil
	}
	if len(models) > r.maxBatchSize {
		return fmt.Errorf("%w: got %d, limit is %d", ErrBatchTooLarge, len(models), r.maxBatchSize)
	}
	items := make([]any, len(models))
	for index, model := range models {
		if isNilDocument(model) {
			return fmt.Errorf("%w: nil document at index %d", ErrInvalidRepository, index)
		}
		items[index] = model
	}
	result, err := r.collection.InsertMany(ctx, items)
	if err != nil {
		return fmt.Errorf("mongodb: create document batch: %w", err)
	}
	for index, id := range result.InsertedIDs {
		if index >= len(models) {
			break
		}
		if objectID, ok := id.(primitive.ObjectID); ok {
			(*models[index]).SetID(objectID)
		}
	}
	return nil
}

// DeleteBulk removes a bounded list of document IDs.
func (r *BaseRepository[T]) DeleteBulk(ctx context.Context, ids []primitive.ObjectID) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > r.maxBatchSize {
		return fmt.Errorf("%w: got %d, limit is %d", ErrBatchTooLarge, len(ids), r.maxBatchSize)
	}
	for index, id := range ids {
		if id.IsZero() {
			return fmt.Errorf("%w: zero id at index %d", ErrInvalidID, index)
		}
	}
	if _, err := r.collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}}); err != nil {
		return fmt.Errorf("mongodb: delete document batch: %w", err)
	}
	return nil
}

func (r *BaseRepository[T]) ready(ctx context.Context) error {
	if r == nil {
		return ErrInvalidRepository
	}
	if r.initErr != nil {
		return r.initErr
	}
	if r.collection == nil {
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

func isNilDocument[T Document](model *T) bool {
	if model == nil {
		return true
	}
	value := reflect.ValueOf(*model)
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

func encodeUpdateDocument[T Document](model *T) (bson.M, error) {
	encoded, err := bson.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEncodeFailed, err)
	}
	update := bson.M{}
	if err := bson.Unmarshal(encoded, &update); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEncodeFailed, err)
	}
	delete(update, "_id")
	if len(update) == 0 {
		return nil, fmt.Errorf("%w: document has no mutable fields", ErrEncodeFailed)
	}
	return update, nil
}

func cursorProvided(cursor any) bool {
	if cursor == nil {
		return false
	}
	if value, ok := cursor.(string); ok {
		return value != ""
	}
	return true
}

func cloneFieldSet(fields map[string]struct{}) map[string]struct{} {
	if fields == nil {
		return nil
	}
	clone := make(map[string]struct{}, len(fields))
	for field := range fields {
		clone[field] = struct{}{}
	}
	return clone
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

func joinCursorError(operationErr, closeErr error) error {
	if closeErr == nil {
		return operationErr
	}
	return errors.Join(operationErr, fmt.Errorf("mongodb: close cursor: %w", closeErr))
}
