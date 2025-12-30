package mongodb

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/huynhanx03/go-common/pkg/dto"
)

type mongoRepository struct {
	db *mongo.Client
}

func NewMongoRepository(db *mongo.Client) *mongoRepository {
	return &mongoRepository{
		db: db,
	}
}

// Document interface that all models must implement
type Document interface {
	GetID() primitive.ObjectID
	SetID(primitive.ObjectID)
	UpdateTimestamp()
}

// Repository defines the common interface for all repositories
type Repository[T Document] interface {
	Find(ctx context.Context, opts *dto.QueryOptions) (*dto.Paginated[T], error)
	Get(ctx context.Context, id primitive.ObjectID) (*T, error)

	Create(ctx context.Context, model *T) error
	Update(ctx context.Context, id primitive.ObjectID, model *T) error
	Delete(ctx context.Context, id primitive.ObjectID) error
	DeleteMany(ctx context.Context, filter bson.M) (int64, error)

	Exists(ctx context.Context, id primitive.ObjectID) (bool, error)
}
