package database

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/dto"
)

// Repository defines the common interface for all repositories
type Repository[T any, ID any] interface {
	Find(ctx context.Context, opts *dto.QueryOptions) (*dto.Paginated[*T], error)
	Get(ctx context.Context, id ID) (*T, error)
	Create(ctx context.Context, model *T) error
	Update(ctx context.Context, model *T) error
	Delete(ctx context.Context, id ID) error
	Exists(ctx context.Context, id ID) (bool, error)
	CreateBulk(ctx context.Context, models []*T) error
	DeleteBulk(ctx context.Context, ids []ID) error
}
