package mongodb

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// BaseModel contains common fields for all models
type BaseModel struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

// NewBaseModel creates a new BaseModel with current timestamp
func NewBaseModel() *BaseModel {
	now := time.Now()
	return &BaseModel{
		ID:        primitive.NewObjectID(),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// GetID returns the ID field
func (b *BaseModel) GetID() primitive.ObjectID {
	return b.ID
}

// SetID sets the ID field
func (b *BaseModel) SetID(id primitive.ObjectID) {
	b.ID = id
}

// UpdateTimestamp updates the UpdatedAt field
func (b *BaseModel) UpdateTimestamp() {
	b.UpdatedAt = time.Now()
}
