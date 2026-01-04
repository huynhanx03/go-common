package ent

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/constraints"
)

// BaseModel contains common fields for all Ent schemas.
type BaseModel[ID constraints.ID] struct {
	ID        ID        `json:"id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// NewBaseModel creates a new BaseModel with current timestamp.
func NewBaseModel[ID constraints.ID](id ID) BaseModel[ID] {
	now := time.Now()
	return BaseModel[ID]{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// GetID returns the ID.
func (m *BaseModel[ID]) GetID() ID {
	return m.ID
}

// SetID sets the ID.
func (m *BaseModel[ID]) SetID(id ID) {
	m.ID = id
}
