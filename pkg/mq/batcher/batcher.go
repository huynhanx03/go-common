package batcher

import (
	"bytes"
	"context"
	"sync"
)

// StripedBatcher retains its historical name but now owns one deterministic,
// bounded buffer. Consumer callbacks run outside the state lock, batches are
// copied before delivery, and Close drains the final partial batch.
type StripedBatcher[T any] struct {
	// pool remains only for source compatibility with historical in-package
	// characterization tests. Production state is explicitly owned below.
	pool *sync.Pool

	consumer Consumer[T]
	capacity int
	buffer   []T
	closed   bool

	stateToken   chan struct{}
	consumeToken chan struct{}
}

// New creates the historical compatibility batcher.
//
// Deprecated: use NewChecked so invalid configuration fails explicitly.
func New[T any](consumer Consumer[T], config Config) *StripedBatcher[T] {
	if config.StripeSize <= 0 {
		config.StripeSize = DefaultBatchSize
	}
	batcher, _ := newBatcher(consumer, config)
	return batcher
}

// NewChecked constructs a bounded batcher and rejects nil consumers and
// invalid capacities.
func NewChecked[T any](
	consumer Consumer[T],
	config Config,
) (*StripedBatcher[T], error) {
	if consumer == nil ||
		config.StripeSize <= 0 ||
		config.StripeSize > MaxBatchSize {
		return nil, ErrInvalidConfig
	}
	return newBatcher(consumer, config)
}

func newBatcher[T any](
	consumer Consumer[T],
	config Config,
) (*StripedBatcher[T], error) {
	if config.StripeSize <= 0 || config.StripeSize > MaxBatchSize {
		return nil, ErrInvalidConfig
	}
	batcher := &StripedBatcher[T]{
		pool:         &sync.Pool{},
		consumer:     consumer,
		capacity:     config.StripeSize,
		buffer:       make([]T, 0, config.StripeSize),
		stateToken:   make(chan struct{}, 1),
		consumeToken: make(chan struct{}, 1),
	}
	batcher.stateToken <- struct{}{}
	batcher.consumeToken <- struct{}{}
	return batcher, nil
}

// Push adds an item using the compatibility fire-and-forget contract.
// Consumer errors are intentionally ignored; use PushContext to observe them.
func (batcher *StripedBatcher[T]) Push(item T) {
	if batcher == nil || batcher.consumer == nil {
		panic(ErrInvalidConfig)
	}
	_ = batcher.PushContext(context.Background(), item)
}

// PushContext admits one owned item and flushes synchronously at capacity.
func (batcher *StripedBatcher[T]) PushContext(
	ctx context.Context,
	item T,
) error {
	if batcher == nil || ctx == nil || batcher.consumer == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	item = cloneRetainedItem(item)
	if err := acquireToken(ctx, batcher.stateToken); err != nil {
		return err
	}
	if batcher.closed {
		releaseToken(batcher.stateToken)
		return ErrClosed
	}
	batcher.buffer = append(batcher.buffer, item)
	if len(batcher.buffer) < batcher.capacity {
		releaseToken(batcher.stateToken)
		return nil
	}
	if err := acquireToken(ctx, batcher.consumeToken); err != nil {
		var zero T
		batcher.buffer[len(batcher.buffer)-1] = zero
		batcher.buffer = batcher.buffer[:len(batcher.buffer)-1]
		releaseToken(batcher.stateToken)
		return err
	}
	batch := batcher.takeBatch()
	releaseToken(batcher.stateToken)
	err := consumeSafely(batcher.consumer, batch)
	releaseToken(batcher.consumeToken)
	return err
}

// Flush delivers the current partial batch without closing the batcher.
func (batcher *StripedBatcher[T]) Flush(ctx context.Context) error {
	return batcher.flush(ctx, false)
}

// Close rejects new items and deterministically drains the final partial
// batch. It is safe to call concurrently and repeatedly.
func (batcher *StripedBatcher[T]) Close(ctx context.Context) error {
	return batcher.flush(ctx, true)
}

func (batcher *StripedBatcher[T]) flush(
	ctx context.Context,
	closeAfter bool,
) error {
	if batcher == nil || ctx == nil || batcher.consumer == nil {
		return ErrInvalidConfig
	}
	if err := acquireToken(ctx, batcher.stateToken); err != nil {
		return err
	}
	if batcher.closed {
		releaseToken(batcher.stateToken)
		return nil
	}
	if closeAfter {
		batcher.closed = true
	}
	if len(batcher.buffer) == 0 {
		releaseToken(batcher.stateToken)
		return nil
	}
	if err := acquireToken(ctx, batcher.consumeToken); err != nil {
		if closeAfter {
			batcher.closed = false
		}
		releaseToken(batcher.stateToken)
		return err
	}
	batch := batcher.takeBatch()
	releaseToken(batcher.stateToken)
	err := consumeSafely(batcher.consumer, batch)
	releaseToken(batcher.consumeToken)
	return err
}

func (batcher *StripedBatcher[T]) takeBatch() []T {
	batch := append([]T(nil), batcher.buffer...)
	clear(batcher.buffer)
	batcher.buffer = batcher.buffer[:0]
	return batch
}

func acquireToken(ctx context.Context, token chan struct{}) error {
	select {
	case <-token:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseToken(token chan struct{}) {
	token <- struct{}{}
}

func consumeSafely[T any](
	consumer Consumer[T],
	batch []T,
) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrConsumerPanic
		}
	}()
	return consumer.Consume(batch)
}

func cloneRetainedItem[T any](item T) T {
	switch value := any(item).(type) {
	case []byte:
		return any(bytes.Clone(value)).(T)
	default:
		return item
	}
}
