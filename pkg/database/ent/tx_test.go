package ent

import (
	"context"
	"errors"
	"testing"
)

type fakeTx struct {
	committed  bool
	rolledBack bool
}

func (t *fakeTx) Commit() error   { t.committed = true; return nil }
func (t *fakeTx) Rollback() error { t.rolledBack = true; return nil }

func beginFake(tx *fakeTx) func(context.Context) (*fakeTx, error) {
	return func(context.Context) (*fakeTx, error) { return tx, nil }
}

func TestWithTxCommitsOnSuccess(t *testing.T) {
	tx := &fakeTx{}
	err := WithTx(context.Background(), beginFake(tx), func(*fakeTx) error { return nil })
	if err != nil || !tx.committed || tx.rolledBack {
		t.Fatalf("err=%v committed=%v rolledBack=%v", err, tx.committed, tx.rolledBack)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	tx := &fakeTx{}
	boom := errors.New("boom")
	err := WithTx(context.Background(), beginFake(tx), func(*fakeTx) error { return boom })
	if !errors.Is(err, boom) || !tx.rolledBack || tx.committed {
		t.Fatalf("err=%v committed=%v rolledBack=%v", err, tx.committed, tx.rolledBack)
	}
}

func TestWithTxRollsBackOnPanic(t *testing.T) {
	tx := &fakeTx{}
	defer func() {
		if recover() == nil {
			t.Fatal("panic must propagate")
		}
		if !tx.rolledBack || tx.committed {
			t.Fatalf("committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
		}
	}()
	_ = WithTx(context.Background(), beginFake(tx), func(*fakeTx) error { panic("boom") })
}

func TestWithTxBeginError(t *testing.T) {
	begin := func(context.Context) (*fakeTx, error) { return nil, errors.New("no conn") }
	if err := WithTx(context.Background(), begin, func(*fakeTx) error { return nil }); err == nil {
		t.Fatal("begin error must propagate")
	}
}

func TestTxManagerInjectsTxIntoContext(t *testing.T) {
	type txCtxKey struct{}

	tx := &fakeTx{}
	manager := NewTxManager(beginFake(tx), func(ctx context.Context, t *fakeTx) context.Context {
		return context.WithValue(ctx, txCtxKey{}, t)
	})

	err := manager.DoInTx(context.Background(), func(ctx context.Context) error {
		if got, _ := ctx.Value(txCtxKey{}).(*fakeTx); got != tx {
			t.Fatal("transaction was not injected into the context")
		}
		return nil
	})
	if err != nil || !tx.committed {
		t.Fatalf("err=%v committed=%v", err, tx.committed)
	}
}
