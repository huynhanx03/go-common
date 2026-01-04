package database

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/dto"
)

// Repository defines the common interface for all repositories
type Repository[T any, ID any] interface {
	Create(ctx context.Context, model *T) error
	Update(ctx context.Context, model *T) error
	Delete(ctx context.Context, id ID) error
	Get(ctx context.Context, id ID) (*T, error)
	Find(ctx context.Context, opts *dto.QueryOptions) (*dto.Paginated[*T], error)

	Exists(ctx context.Context, id ID) (bool, error)

	BatchCreate(ctx context.Context, models []*T) error
	BatchDelete(ctx context.Context, ids []ID) error
}
