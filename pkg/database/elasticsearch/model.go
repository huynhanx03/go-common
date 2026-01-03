package elasticsearch

import "time"

// BaseModel contains common fields for all Elasticsearch documents
type BaseModel struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewBaseModel creates a new BaseModel with current timestamp
func NewBaseModel(id string) BaseModel {
	now := time.Now()
	return BaseModel{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// GetID returns the document ID
func (b *BaseModel) GetID() string {
	return b.ID
}

// SetID sets the document ID
func (b *BaseModel) SetID(id string) {
	b.ID = id
}
