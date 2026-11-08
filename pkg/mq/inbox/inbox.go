// Package inbox provides transactional deduplication for message consumers.
package inbox

import (
	"context"
	"errors"
	"strings"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

// Key scopes a source message ID to one logical consumer.
type Key struct {
	Consumer  string
	MessageID string
}

// Transaction exposes the transaction context used by the application's
// repositories while processing one message.
type Transaction interface {
	Context() context.Context
	Commit() error
	Rollback() error
}

// Store atomically claims a message key and starts its application transaction.
// Begin returns duplicate=true with no transaction when the key is already
// committed. Otherwise, it returns a non-nil transaction and the inbox row and
// application writes must share it. Begin must roll back failed uniqueness
// transactions before returning duplicate; on other errors it cleans up any
// transaction it opened.
type Store interface {
	Begin(context.Context, Key) (transaction Transaction, duplicate bool, err error)
}

// Processor adds inbox deduplication for one logical consumer.
type Processor struct {
	store    Store
	consumer string
}

// New creates a processor with a stable name that scopes deduplication rows.
func New(store Store, consumerName string) (*Processor, error) {
	consumerName = strings.TrimSpace(consumerName)
	if store == nil || consumerName == "" {
		return nil, ErrInvalidConfig
	}
	return &Processor{store: store, consumer: consumerName}, nil
}

// Middleware skips committed duplicates and commits the application's
// transaction only when the handler succeeds.
func (processor *Processor) Middleware() consumer.Middleware {
	return func(next consumer.Handler) consumer.Handler {
		return func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
			if message.ID == "" {
				return consumer.Retry, ErrMissingMessageID
			}
			if !consumer.IsValidMessageID(message.ID) {
				return consumer.Retry, ErrInvalidMessageID
			}

			transaction, duplicate, err := processor.store.Begin(ctx, Key{
				Consumer:  processor.consumer,
				MessageID: message.ID,
			})
			if err != nil {
				return consumer.Retry, err
			}
			if duplicate {
				return consumer.Success, nil
			}
			if transaction == nil {
				return consumer.Retry, ErrInvalidTransaction
			}
			transactionContext := transaction.Context()
			if transactionContext == nil {
				return consumer.Retry, errors.Join(ErrInvalidTransaction, transaction.Rollback())
			}

			settled := false
			defer func() {
				if !settled {
					_ = transaction.Rollback()
				}
			}()

			outcome, handlerErr := next(transactionContext, message)
			if outcome == consumer.Success && handlerErr == nil {
				if err := transaction.Commit(); err != nil {
					rollbackErr := transaction.Rollback()
					settled = true
					return consumer.Retry, errors.Join(err, rollbackErr)
				}
				settled = true
				return consumer.Success, nil
			}

			if outcome != consumer.Success && outcome != consumer.Retry && outcome != consumer.DeadLetter {
				handlerErr = errors.Join(consumer.ErrInvalidOutcome, handlerErr)
				outcome = consumer.Retry
			}

			rollbackErr := transaction.Rollback()
			settled = true
			if rollbackErr != nil {
				return consumer.Retry, errors.Join(handlerErr, rollbackErr)
			}

			switch outcome {
			case consumer.Success:
				return consumer.Retry, handlerErr
			case consumer.Retry:
				return consumer.Retry, handlerErr
			case consumer.DeadLetter:
				return consumer.DeadLetter, handlerErr
			default:
				return consumer.Retry, errors.Join(consumer.ErrInvalidOutcome, handlerErr)
			}
		}
	}
}
