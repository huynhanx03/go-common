package mongodb

import (
	"github.com/huynhanx03/go-common/pkg/database"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
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

// Repository aliases the common interface
type Repository[T Document] database.Repository[T, primitive.ObjectID]
