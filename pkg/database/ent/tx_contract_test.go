package ent

import (
	"context"
	"errors"
	"testing"
)

type failingTx struct {
	commitErr   error
	rollbackErr error
}

func (transaction *failingTx) Commit() error   { return transaction.commitErr }
func (transaction *failingTx) Rollback() error { return transaction.rollbackErr }

func TestWithTxPreservesCallbackAndRollbackErrors(t *testing.T) {
	t.Parallel()

	callbackErr := errors.New("callback failed")
	rollbackErr := errors.New("rollback failed")
	transaction := &failingTx{rollbackErr: rollbackErr}
	err := WithTx(
		context.Background(),
		func(context.Context) (*failingTx, error) { return transaction, nil },
		func(*failingTx) error { return callbackErr },
	)
	if !errors.Is(err, callbackErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("transaction error = %v", err)
	}
}

func TestWithTxRejectsInvalidOrCanceledContractBeforeBegin(t *testing.T) {
	t.Parallel()

	if err := WithTx[*failingTx](nil, nil, nil); !errors.Is(
		err,
		ErrInvalidTransaction,
	) {
		t.Fatalf("invalid contract error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := WithTx(
		ctx,
		func(context.Context) (*failingTx, error) {
			called = true
			return &failingTx{}, nil
		},
		func(*failingTx) error { return nil },
	)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled error=%v begin called=%v", err, called)
	}
}

func TestWithTxRejectsNilInterfaceTransaction(t *testing.T) {
	t.Parallel()

	callbackCalled := false
	err := WithTx[Tx](
		context.Background(),
		func(context.Context) (Tx, error) { return nil, nil },
		func(Tx) error {
			callbackCalled = true
			return nil
		},
	)
	if !errors.Is(err, ErrInvalidTransaction) || callbackCalled {
		t.Fatalf("nil transaction error=%v callback called=%v", err, callbackCalled)
	}
}

func TestWithTxPanicRetainsRollbackFailure(t *testing.T) {
	t.Parallel()

	rollbackErr := errors.New("rollback failed")
	defer func() {
		recovered := recover()
		var panicError *TransactionPanicError
		if !errors.As(asError(recovered), &panicError) ||
			!errors.Is(panicError, rollbackErr) {
			t.Fatalf("recovered = %#v", recovered)
		}
	}()
	_ = WithTx(
		context.Background(),
		func(context.Context) (*failingTx, error) {
			return &failingTx{rollbackErr: rollbackErr}, nil
		},
		func(*failingTx) error {
			panic("sensitive panic value")
		},
	)
}

func asError(value any) error {
	err, _ := value.(error)
	return err
}
