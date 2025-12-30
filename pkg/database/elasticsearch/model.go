package elasticsearch

import "time"

// BaseDocument contains common fields for all Elasticsearch documents
type BaseDocument struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewBaseDocument creates a new BaseDocument with current timestamp
func NewBaseDocument(id string) BaseDocument {
	now := time.Now()
	return BaseDocument{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// GetID returns the document ID
func (b *BaseDocument) GetID() string {
	return b.ID
}

// SetID sets the document ID
func (b *BaseDocument) SetID(id string) {
	b.ID = id
}
