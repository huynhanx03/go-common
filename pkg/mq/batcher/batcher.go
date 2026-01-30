package batcher

import (
	"sync"
)

// StripedBatcher is a high-performance, concurrent batcher using striped buffers.
// It leverages sync.Pool to reduce contention (mutex-free mostly) and allocations.
//
// Behavior:
//   - Multiple goroutines can call Push() concurrently.
//   - Items are batched into local "stripes" (buffers) per P (processor) ideally.
//   - When a stripe is full, it is flushed to the Consumer immediately.
//   - This is a "Lossy" design regarding graceful shutdown: items pending in stripes
//     inside the pool are NOT guaranteed to be flushed on shutdown unless Consumer
//     handles tracking. Use this for metrics, logs, or cache events where speed > absolute precision.
type StripedBatcher[T any] struct {
	pool *sync.Pool
}

// New creates a new StripedBatcher for type T.
func New[T any](cons Consumer[T], cfg Config) *StripedBatcher[T] {
	// Default config
	if cfg.StripeSize <= 0 {
		cfg.StripeSize = 512
	}

	return &StripedBatcher[T]{
		pool: &sync.Pool{
			New: func() any {
				return newStripe[T](cons, cfg.StripeSize)
			},
		},
	}
}

// Push adds an item to the batcher.
// It may trigger a flush to Consumer if the underlying stripe becomes full.
func (b *StripedBatcher[T]) Push(item T) {
	// 1. Get a local stripe from the pool.
	//    This effectively picks a buffer associated with the current P (goroutine),
	//    minimizing contention.
	s := b.pool.Get().(*stripe[T])

	// 2. Push item to the stripe (not thread-safe, but we own it right now).
	s.Push(item)

	// 3. Return stripe to the pool.
	b.pool.Put(s)
}
