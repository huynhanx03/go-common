package batcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewCheckedRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := NewChecked[int](nil, Config{StripeSize: 1}); !errors.Is(
		err,
		ErrInvalidConfig,
	) {
		t.Fatalf("nil consumer error = %v", err)
	}
	consumer := &mockConsumer[int]{}
	for _, size := range []int{0, -1, MaxBatchSize + 1} {
		if _, err := NewChecked[int](consumer, Config{StripeSize: size}); !errors.Is(
			err,
			ErrInvalidConfig,
		) {
			t.Fatalf("size %d error = %v", size, err)
		}
	}
}

type retainingConsumer struct {
	mu      sync.Mutex
	batches [][]byte
}

func (consumer *retainingConsumer) Consume(batch [][]byte) error {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	consumer.batches = append(consumer.batches, batch...)
	return nil
}

func TestPushContextCopiesRetainedByteSlicesAndCloseDrains(t *testing.T) {
	t.Parallel()

	consumer := &retainingConsumer{}
	batcher, err := NewChecked[[]byte](consumer, Config{StripeSize: 4})
	if err != nil {
		t.Fatalf("NewChecked: %v", err)
	}
	value := []byte("original")
	if err := batcher.PushContext(context.Background(), value); err != nil {
		t.Fatalf("PushContext: %v", err)
	}
	value[0] = 'X'
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := batcher.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if len(consumer.batches) != 1 ||
		string(consumer.batches[0]) != "original" {
		t.Fatalf("retained batches = %q", consumer.batches)
	}
	if err := batcher.PushContext(context.Background(), []byte("late")); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("late PushContext error = %v", err)
	}
}

type panicConsumer struct{}

func (panicConsumer) Consume([]int) error {
	panic("api-key=secret")
}

func TestBatcherContainsConsumerPanicAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	batcher, err := NewChecked[int](panicConsumer{}, Config{StripeSize: 1})
	if err != nil {
		t.Fatalf("NewChecked: %v", err)
	}
	if err := batcher.PushContext(context.Background(), 1); !errors.Is(
		err,
		ErrConsumerPanic,
	) {
		t.Fatalf("panic error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := batcher.PushContext(ctx, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PushContext error = %v", err)
	}
}
