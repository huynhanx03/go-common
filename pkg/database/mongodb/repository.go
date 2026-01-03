package mongodb

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/huynhanx03/go-common/pkg/dto"
)

// BaseRepository provides common database operations using generics
type BaseRepository[T Document] struct {
	collection *mongo.Collection
	timeout    time.Duration
}

var _ Repository[*BaseModel] = (*BaseRepository[*BaseModel])(nil)

// NewBaseRepository creates a new base repository
func NewBaseRepository[T Document](collection *mongo.Collection) *BaseRepository[T] {
	return &BaseRepository[T]{
		collection: collection,
		timeout:    30 * time.Second,
	}
}

// GetContext creates a context with timeout
func (r *BaseRepository[T]) GetContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), r.timeout)
}

// GetCollection returns the MongoDB collection
func (r *BaseRepository[T]) GetCollection() *mongo.Collection {
	return r.collection
}

// Get retrieves a document by ID
func (r *BaseRepository[T]) Get(ctx context.Context, id primitive.ObjectID) (*T, error) {
	var model T

	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&model)
	if err != nil {
		return nil, err
	}

	return &model, nil
}

// Create inserts a new document
func (r *BaseRepository[T]) Create(ctx context.Context, model *T) error {
	res, err := r.collection.InsertOne(ctx, model)
	if err != nil {
		return err
	}

	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		(*model).SetID(oid)
	}

	return nil
}

// Update updates a document by ID
func (r *BaseRepository[T]) Update(ctx context.Context, model *T) error {
	(*model).UpdateTimestamp()

	update := bson.M{"$set": model}
	_, err := r.collection.UpdateOne(ctx, bson.M{"_id": (*model).GetID()}, update)
	return err
}

// Delete removes a document by ID
func (r *BaseRepository[T]) Delete(ctx context.Context, id primitive.ObjectID) error {
	_, err := r.collection.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

// Exists checks whether a document exists by its ID
func (r *BaseRepository[T]) Exists(ctx context.Context, id primitive.ObjectID) (bool, error) {
	count, err := r.collection.CountDocuments(ctx, bson.M{"_id": id})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Find retrieves documents with pagination, search/filter, and sorting
func (r *BaseRepository[T]) Find(ctx context.Context, opts *dto.QueryOptions) (*dto.Paginated[T], error) {
	if opts == nil {
		opts = &dto.QueryOptions{}
	}
	if opts.Pagination == nil {
		opts.Pagination = &dto.PaginationOptions{}
	}
	opts.Pagination.SetDefaults()

	// Build filter from search/filter options
	filter := BuildFilter(&opts.Filters)

	// Cursor pagination: if cursor is present, filter by _id < cursor (assuming desc sort)
	if opts.Pagination.Cursor != "" {
		if oid, err := primitive.ObjectIDFromHex(opts.Pagination.Cursor); err == nil {
			filter["_id"] = bson.M{"$lt": oid}
		}
	}

	// Count total documents
	totalItems, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Calculate pagination info
	pagination := dto.CalculatePagination(opts.Pagination.Page, opts.Pagination.PageSize, totalItems)

	// Build sort from sort options
	sort := BuildSort(&opts.Sort)

	// Find documents with pagination and sorting
	findOpts := GetPaginationOptions(opts.Pagination)
	findOpts.SetSort(sort)

	cursor, err := r.collection.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	// Decode documents
	var records []T
	if err := cursor.All(ctx, &records); err != nil {
		return nil, err
	}

	return &dto.Paginated[T]{
		Records:    &records,
		Pagination: pagination,
	}, nil
}

// BatchCreate inserts multiple documents
func (r *BaseRepository[T]) BatchCreate(ctx context.Context, models []*T) error {
	if len(models) == 0 {
		return nil
	}

	// mongo-driver InsertMany requires []interface{}
	items := make([]interface{}, len(models))
	for i, v := range models {
		items[i] = v
	}

	res, err := r.collection.InsertMany(ctx, items)
	if err != nil {
		return err
	}

	for i, id := range res.InsertedIDs {
		if oid, ok := id.(primitive.ObjectID); ok {
			(*models[i]).SetID(oid)
		}
	}

	return nil
}

// BatchDelete removes multiple documents by IDs
func (r *BaseRepository[T]) BatchDelete(ctx context.Context, ids []primitive.ObjectID) error {
	if len(ids) == 0 {
		return nil
	}

	_, err := r.collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	return err
}
