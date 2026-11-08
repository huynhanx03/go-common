package inbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
	"github.com/huynhanx03/go-common/pkg/mq/inbox"
)

func TestMiddlewareCommitsSuccessfulWorkAndSkipsCommittedDuplicates(t *testing.T) {
	t.Run("new message commits the handler transaction", func(t *testing.T) {
		transactionContext := context.WithValue(context.Background(), struct{}{}, "transaction")
		transaction := &fakeTransaction{ctx: transactionContext}
		store := &fakeStore{transaction: transaction}
		middleware := newMiddleware(t, store, "  notifications  ")
		called := false
		handler := middleware(func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
			called = true
			if ctx.Value(struct{}{}) != "transaction" {
				t.Fatal("handler did not receive the store transaction context")
			}
			if message.ID != "event-42" {
				t.Fatalf("handler message ID = %q", message.ID)
			}
			return consumer.Success, nil
		})
		outcome, err := handler(context.Background(), consumer.Message{ID: "event-42"})
		if outcome != consumer.Success || err != nil || !called {
			t.Fatalf("result = (%v, %v), called=%t", outcome, err, called)
		}
		if store.key != (inbox.Key{Consumer: "notifications", MessageID: "event-42"}) {
			t.Fatalf("claimed key = %+v", store.key)
		}
		if transaction.commits != 1 || transaction.rollbacks != 0 {
			t.Fatalf("transaction settled with commits=%d rollbacks=%d", transaction.commits, transaction.rollbacks)
		}
	})

	t.Run("duplicate skips business handler", func(t *testing.T) {
		store := &fakeStore{duplicate: true}
		handler := newMiddleware(t, store, "notifications")(func(context.Context, consumer.Message) (consumer.Outcome, error) {
			t.Fatal("duplicate message reached business handler")
			return consumer.Retry, nil
		})
		outcome, err := handler(context.Background(), consumer.Message{ID: "event-42"})
		if outcome != consumer.Success || err != nil {
			t.Fatalf("duplicate result = (%v, %v)", outcome, err)
		}
		if store.begins != 1 {
			t.Fatalf("store Begin calls = %d", store.begins)
		}
	})
}

func TestMiddlewareRejectsMessagesWithoutSafeIdentityBeforeOpeningTransaction(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
		want error
	}{
		{name: "missing identity", want: inbox.ErrMissingMessageID},
		{name: "unsafe identity", id: "event\x00id", want: inbox.ErrInvalidMessageID},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{}
			handler := newMiddleware(t, store, "notifications")(func(context.Context, consumer.Message) (consumer.Outcome, error) {
				t.Fatal("message without a safe identity reached business handler")
				return consumer.Success, nil
			})
			outcome, err := handler(context.Background(), consumer.Message{ID: test.id})
			if outcome != consumer.Retry || !errors.Is(err, test.want) {
				t.Fatalf("result = (%v, %v), want retry with %v", outcome, err, test.want)
			}
			if store.begins != 0 {
				t.Fatalf("opened %d transactions for invalid identity", store.begins)
			}
		})
	}
}

func TestMiddlewareRetriesAndRollsBackUnsuccessfulHandlerOutcomes(t *testing.T) {
	failure := errors.New("business operation failed")
	for _, test := range []struct {
		name    string
		outcome consumer.Outcome
		handler error
		want    consumer.Outcome
		wantErr error
	}{
		{name: "retry", outcome: consumer.Retry, handler: failure, want: consumer.Retry, wantErr: failure},
		{name: "success with error is retried", outcome: consumer.Success, handler: failure, want: consumer.Retry, wantErr: failure},
		{name: "dead letter is preserved", outcome: consumer.DeadLetter, handler: failure, want: consumer.DeadLetter, wantErr: failure},
		{name: "unknown outcome retries", outcome: consumer.Outcome(99), want: consumer.Retry, wantErr: consumer.ErrInvalidOutcome},
		{name: "rollback failure retries even after dead letter", outcome: consumer.DeadLetter, handler: failure, want: consumer.Retry, wantErr: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			rollbackFailure := error(nil)
			if test.name == "rollback failure retries even after dead letter" {
				rollbackFailure = errors.New("rollback failed")
			}
			transaction := &fakeTransaction{ctx: context.Background(), rollbackErr: rollbackFailure}
			store := &fakeStore{transaction: transaction}
			handler := newMiddleware(t, store, "notifications")(func(context.Context, consumer.Message) (consumer.Outcome, error) {
				return test.outcome, test.handler
			})
			outcome, err := handler(context.Background(), consumer.Message{ID: "event-42"})
			if outcome != test.want || !errors.Is(err, test.wantErr) {
				t.Fatalf("result = (%v, %v), want (%v, %v)", outcome, err, test.want, test.wantErr)
			}
			if rollbackFailure != nil && !errors.Is(err, rollbackFailure) {
				t.Fatalf("rollback cause missing from %v", err)
			}
			if transaction.commits != 0 || transaction.rollbacks != 1 {
				t.Fatalf("transaction settled with commits=%d rollbacks=%d", transaction.commits, transaction.rollbacks)
			}
		})
	}
}

func TestMiddlewareCleansUpStoreAndTransactionFailures(t *testing.T) {
	storeFailure := errors.New("database unavailable")
	t.Run("Begin failure is retried", func(t *testing.T) {
		store := &fakeStore{beginErr: storeFailure}
		handler := newMiddleware(t, store, "notifications")(func(context.Context, consumer.Message) (consumer.Outcome, error) {
			t.Fatal("handler ran after failed transaction begin")
			return consumer.Success, nil
		})
		outcome, err := handler(context.Background(), consumer.Message{ID: "event-42"})
		if outcome != consumer.Retry || !errors.Is(err, storeFailure) {
			t.Fatalf("result = (%v, %v)", outcome, err)
		}
	})

	t.Run("missing transaction is retried", func(t *testing.T) {
		store := &fakeStore{}
		handler := newMiddleware(t, store, "notifications")(func(context.Context, consumer.Message) (consumer.Outcome, error) {
			t.Fatal("handler ran without a transaction")
			return consumer.Success, nil
		})
		outcome, err := handler(context.Background(), consumer.Message{ID: "event-42"})
		if outcome != consumer.Retry || !errors.Is(err, inbox.ErrInvalidTransaction) {
			t.Fatalf("result = (%v, %v)", outcome, err)
		}
	})

	t.Run("invalid transaction context is rolled back", func(t *testing.T) {
		rollbackFailure := errors.New("rollback failed")
		transaction := &fakeTransaction{rollbackErr: rollbackFailure}
		store := &fakeStore{transaction: transaction}
		handler := newMiddleware(t, store, "notifications")(func(context.Context, consumer.Message) (consumer.Outcome, error) {
			t.Fatal("handler ran with a nil transaction context")
			return consumer.Success, nil
		})
		outcome, err := handler(context.Background(), consumer.Message{ID: "event-42"})
		if outcome != consumer.Retry || !errors.Is(err, inbox.ErrInvalidTransaction) || !errors.Is(err, rollbackFailure) {
			t.Fatalf("result = (%v, %v)", outcome, err)
		}
		if transaction.rollbacks != 1 {
			t.Fatalf("rollback calls = %d", transaction.rollbacks)
		}
	})

	t.Run("commit failure rolls back for redelivery", func(t *testing.T) {
		commitFailure, rollbackFailure := errors.New("commit failed"), errors.New("rollback failed")
		transaction := &fakeTransaction{ctx: context.Background(), commitErr: commitFailure, rollbackErr: rollbackFailure}
		store := &fakeStore{transaction: transaction}
		handler := newMiddleware(t, store, "notifications")(func(context.Context, consumer.Message) (consumer.Outcome, error) {
			return consumer.Success, nil
		})
		outcome, err := handler(context.Background(), consumer.Message{ID: "event-42"})
		if outcome != consumer.Retry || !errors.Is(err, commitFailure) || !errors.Is(err, rollbackFailure) {
			t.Fatalf("result = (%v, %v)", outcome, err)
		}
		if transaction.commits != 1 || transaction.rollbacks != 1 {
			t.Fatalf("transaction settled with commits=%d rollbacks=%d", transaction.commits, transaction.rollbacks)
		}
	})
}

func TestMiddlewareRollsBackWhenBusinessHandlerPanics(t *testing.T) {
	transaction := &fakeTransaction{ctx: context.Background()}
	handler := newMiddleware(t, &fakeStore{transaction: transaction}, "notifications")(
		func(context.Context, consumer.Message) (consumer.Outcome, error) { panic("handler bug") },
	)
	func() {
		defer func() {
			if recovered := recover(); recovered != "handler bug" {
				t.Fatalf("panic = %v", recovered)
			}
		}()
		_, _ = handler(context.Background(), consumer.Message{ID: "event-42"})
	}()
	if transaction.commits != 0 || transaction.rollbacks != 1 {
		t.Fatalf("panic left transaction commits=%d rollbacks=%d", transaction.commits, transaction.rollbacks)
	}
}

func TestNewRequiresStoreAndStableConsumerName(t *testing.T) {
	if _, err := inbox.New(nil, "notifications"); !errors.Is(err, inbox.ErrInvalidConfig) {
		t.Fatalf("nil store error = %v", err)
	}
	if _, err := inbox.New(&fakeStore{}, " \t "); !errors.Is(err, inbox.ErrInvalidConfig) {
		t.Fatalf("empty consumer name error = %v", err)
	}
}

func newMiddleware(t *testing.T, store *fakeStore, name string) consumer.Middleware {
	t.Helper()
	processor, err := inbox.New(store, name)
	if err != nil {
		t.Fatal(err)
	}
	return processor.Middleware()
}

type fakeStore struct {
	transaction *fakeTransaction
	duplicate   bool
	beginErr    error
	key         inbox.Key
	begins      int
}

func (store *fakeStore) Begin(_ context.Context, key inbox.Key) (inbox.Transaction, bool, error) {
	store.begins++
	store.key = key
	if store.transaction == nil {
		return nil, store.duplicate, store.beginErr
	}
	return store.transaction, store.duplicate, store.beginErr
}

type fakeTransaction struct {
	ctx         context.Context
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (transaction *fakeTransaction) Context() context.Context { return transaction.ctx }

func (transaction *fakeTransaction) Commit() error {
	transaction.commits++
	return transaction.commitErr
}

func (transaction *fakeTransaction) Rollback() error {
	transaction.rollbacks++
	return transaction.rollbackErr
}
