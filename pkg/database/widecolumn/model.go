package widecolumn

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/constraints"
)

const (
	IDColumn        = "id"
	CreatedAtColumn = "created_at"
	UpdatedAtColumn = "updated_at"
)

type BaseModel[ID constraints.ID] struct {
	ID        ID        `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Model interface {
	TableName() string
	ColumnNames() []string
	ColumnValues() []any
}
