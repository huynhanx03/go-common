package elasticsearch

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/constraints"
)

// BaseModel contains common fields for all Elasticsearch documents
type BaseModel[ID constraints.ID] struct {
	ID        ID        `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewBaseModel creates a new BaseModel with current timestamp
func NewBaseModel[ID constraints.ID](id ID) BaseModel[ID] {
	now := time.Now()
	return BaseModel[ID]{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// GetID returns the document ID
func (b *BaseModel[ID]) GetID() ID {
	return b.ID
}

// SetID sets the document ID
func (b *BaseModel[ID]) SetID(id ID) {
	b.ID = id
}
