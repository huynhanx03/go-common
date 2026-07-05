package ent

import (
	"context"
	"fmt"

	"github.com/huynhanx03/go-common/pkg/common/tx"
)

// Tx is the behavior WithTx needs from a generated *ent.Tx.
type Tx interface {
	Commit() error
	Rollback() error
}

// WithTx runs fn inside a transaction: commit on success, rollback on error,
// rollback and re-panic on panic. begin is usually the generated client's Tx
// method:
//
//	err := ent.WithTx(ctx, client.Tx, func(t *gen.Tx) error {
//		if _, err := t.User.Create().Save(ctx); err != nil {
//			return err
//		}
//		return t.Wallet.UpdateOneID(id).AddBalance(-10).Exec(ctx)
//	})
func WithTx[T Tx](ctx context.Context, begin func(context.Context) (T, error), fn func(T) error) error {
	t, err := begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if v := recover(); v != nil {
			_ = t.Rollback()
			panic(v)
		}
	}()
	if err := fn(t); err != nil {
		if rerr := t.Rollback(); rerr != nil {
			return fmt.Errorf("%w: rolling back transaction: %v", err, rerr)
		}
		return err
	}
	if err := t.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// NewTxManager adapts a generated ent client to the shared tx.Manager
// interface. inject stores the transactional client in the context so
// repositories resolve it instead of the root client:
//
//	manager := ent.NewTxManager(client.Tx, func(ctx context.Context, t *gen.Tx) context.Context {
//		return gen.NewTxContext(ctx, t)
//	})
func NewTxManager[T Tx](
	begin func(context.Context) (T, error),
	inject func(context.Context, T) context.Context,
) tx.Manager {
	return txManager[T]{begin: begin, inject: inject}
}

type txManager[T Tx] struct {
	begin  func(context.Context) (T, error)
	inject func(context.Context, T) context.Context
}

func (m txManager[T]) DoInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return WithTx(ctx, m.begin, func(t T) error {
		return fn(m.inject(ctx, t))
	})
}
