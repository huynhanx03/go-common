package ent

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/huynhanx03/go-common/pkg/common/tx"
)

var ErrInvalidTransaction = errors.New("ent: invalid transaction contract")

// TransactionPanicError preserves a rollback failure when a callback panics
// without rendering the recovered value in Error().
type TransactionPanicError struct {
	Recovered   any
	RollbackErr error
}

func (*TransactionPanicError) Error() string {
	return "ent: transaction callback panicked and rollback failed"
}

func (err *TransactionPanicError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.RollbackErr
}

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
	if ctx == nil || begin == nil || fn == nil {
		return ErrInvalidTransaction
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	t, err := begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if nilTransaction(t) {
		return ErrInvalidTransaction
	}
	defer func() {
		if v := recover(); v != nil {
			if rollbackErr := t.Rollback(); rollbackErr != nil {
				panic(&TransactionPanicError{
					Recovered:   v,
					RollbackErr: rollbackErr,
				})
			}
			panic(v)
		}
	}()
	if err := fn(t); err != nil {
		if rerr := t.Rollback(); rerr != nil {
			return errors.Join(
				err,
				fmt.Errorf("rollback transaction: %w", rerr),
			)
		}
		return err
	}
	if err := t.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func nilTransaction[T Tx](transaction T) bool {
	value := reflect.ValueOf(transaction)
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
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
	if ctx == nil || fn == nil || m.begin == nil || m.inject == nil {
		return ErrInvalidTransaction
	}
	return WithTx(ctx, m.begin, func(t T) error {
		transactionContext := m.inject(ctx, t)
		if transactionContext == nil {
			return ErrInvalidTransaction
		}
		return fn(transactionContext)
	})
}
