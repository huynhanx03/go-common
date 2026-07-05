package tx

import "context"

type Manager interface {
	DoInTx(ctx context.Context, fn func(ctx context.Context) error) error
}
