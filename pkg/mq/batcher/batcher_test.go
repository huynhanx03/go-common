package batcher

import (
	"sync"
	"sync/atomic"
	"testing"
)

// mockConsumer is a test Consumer that tracks received batches.
type mockConsumer[T any] struct {
	mu      sync.Mutex
	batches [][]T
	calls   atomic.Int32
	err     error // error to return from Consume
}

// Consume implements Consumer interface.
func (m *mockConsumer[T]) Consume(batch []T) error {
	m.calls.Add(1)

	// Make a copy to ensure we own the data
	copied := make([]T, len(batch))
	copy(copied, batch)

	m.mu.Lock()
	m.batches = append(m.batches, copied)
	m.mu.Unlock()

	return m.err
}

// totalItems returns the total number of items received across all batches.
func (m *mockConsumer[T]) totalItems() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := 0
	for _, b := range m.batches {
		total += len(b)
	}
	return total
}

// --- Constructor Tests ---

func TestNew(t *testing.T) {
	tests := []struct {
		name       string
		stripeSize int
		wantSize   int // expected effective stripe size
	}{
		{
			name:       "valid_stripe_size",
			stripeSize: 100,
			wantSize:   100,
		},
		{
			name:       "zero_defaults_to_512",
			stripeSize: 0,
			wantSize:   512,
		},
		{
			name:       "negative_defaults_to_512",
			stripeSize: -1,
			wantSize:   512,
		},
		{
			name:       "empty_config_defaults_to_512",
			stripeSize: 0,
			wantSize:   512,
		},
		{
			name:       "small_stripe_size",
			stripeSize: 1,
			wantSize:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cons := &mockConsumer[int]{}
			cfg := Config{StripeSize: tt.stripeSize}

			b := New[int](cons, cfg)

			if b == nil {
				t.Fatal("expected non-nil batcher")
			}
			if b.pool == nil {
				t.Fatal("expected non-nil pool")
			}

			// Verify effective stripe size by pushing exactly wantSize items
			// and checking if flush happens
			for i := 0; i < tt.wantSize; i++ {
				b.Push(i)
			}

			// After pushing exactly wantSize items, should have flushed once
			if cons.calls.Load() != 1 {
				t.Errorf("expected 1 flush after %d items, got %d", tt.wantSize, cons.calls.Load())
			}
		})
	}
}

func TestNew_NilConsumer(t *testing.T) {
	// Creating batcher with nil consumer should work
	// Panic happens on flush, not on creation
	cfg := Config{StripeSize: 2}
	b := New[int](nil, cfg)

	if b == nil {
		t.Fatal("expected non-nil batcher even with nil consumer")
	}

	// Attempting to trigger flush should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when flushing with nil consumer")
		}
	}()

	// Push enough to trigger flush
	b.Push(1)
	b.Push(2)
}

// --- Push Tests ---

func TestPush_SingleItem(t *testing.T) {
	cons := &mockConsumer[string]{}
	b := New[string](cons, Config{StripeSize: 10})

	b.Push("item1")

	// No flush should happen with just 1 item
	if cons.calls.Load() != 0 {
		t.Errorf("expected 0 flushes, got %d", cons.calls.Load())
	}
}

func TestPush_MultipleItemsNoFlush(t *testing.T) {
	cons := &mockConsumer[int]{}
	b := New[int](cons, Config{StripeSize: 10})

	for i := 0; i < 5; i++ {
		b.Push(i)
	}

	// Still no flush (5 < 10)
	if cons.calls.Load() != 0 {
		t.Errorf("expected 0 flushes, got %d", cons.calls.Load())
	}
}

func TestPush_ExactCapTriggerFlush(t *testing.T) {
	cons := &mockConsumer[int]{}
	b := New[int](cons, Config{StripeSize: 5})

	for i := 0; i < 5; i++ {
		b.Push(i)
	}

	// Exactly cap items should trigger 1 flush
	if cons.calls.Load() != 1 {
		t.Errorf("expected 1 flush, got %d", cons.calls.Load())
	}

	if len(cons.batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(cons.batches))
	}

	if len(cons.batches[0]) != 5 {
		t.Errorf("expected batch size 5, got %d", len(cons.batches[0]))
	}
}

func TestPush_CapPlusOneTriggerFlush(t *testing.T) {
	cons := &mockConsumer[int]{}
	cap := 5
	b := New[int](cons, Config{StripeSize: cap})

	for i := 0; i < cap+1; i++ {
		b.Push(i)
	}

	// cap items trigger flush, +1 item goes to new stripe
	if cons.calls.Load() != 1 {
		t.Errorf("expected 1 flush, got %d", cons.calls.Load())
	}
}

func TestPush_AfterFlush(t *testing.T) {
	cons := &mockConsumer[int]{}
	cap := 3
	b := New[int](cons, Config{StripeSize: cap})

	// First batch
	for i := 0; i < cap; i++ {
		b.Push(i)
	}

	if cons.calls.Load() != 1 {
		t.Errorf("expected 1 flush after first batch, got %d", cons.calls.Load())
	}

	// Second batch
	for i := 0; i < cap; i++ {
		b.Push(i + 100)
	}

	if cons.calls.Load() != 2 {
		t.Errorf("expected 2 flushes after second batch, got %d", cons.calls.Load())
	}
}

func TestPush_StripeSize1(t *testing.T) {
	cons := &mockConsumer[int]{}
	b := New[int](cons, Config{StripeSize: 1})

	// Each push should trigger a flush
	for i := 0; i < 5; i++ {
		b.Push(i)
	}

	if cons.calls.Load() != 5 {
		t.Errorf("expected 5 flushes with StripeSize=1, got %d", cons.calls.Load())
	}
}

func TestPush_TwiceCapacity(t *testing.T) {
	cons := &mockConsumer[int]{}
	cap := 4
	b := New[int](cons, Config{StripeSize: cap})

	for i := 0; i < cap*2; i++ {
		b.Push(i)
	}

	if cons.calls.Load() != 2 {
		t.Errorf("expected 2 flushes after 2*cap items, got %d", cons.calls.Load())
	}
}

// --- Flush Behavior Tests ---

func TestFlush_BatchContent(t *testing.T) {
	cons := &mockConsumer[int]{}
	cap := 3
	b := New[int](cons, Config{StripeSize: cap})

	// Push specific values
	b.Push(10)
	b.Push(20)
	b.Push(30)

	if len(cons.batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(cons.batches))
	}

	batch := cons.batches[0]
	expected := []int{10, 20, 30}

	if len(batch) != len(expected) {
		t.Fatalf("expected batch len %d, got %d", len(expected), len(batch))
	}

	for i, v := range expected {
		if batch[i] != v {
			t.Errorf("batch[%d] = %d, want %d", i, batch[i], v)
		}
	}
}

func TestFlush_ConsumerErrorIgnored(t *testing.T) {
	cons := &mockConsumer[int]{err: errTest}
	b := New[int](cons, Config{StripeSize: 2})

	// Push should not panic even if consumer returns error
	b.Push(1)
	b.Push(2) // triggers flush with error

	// Should continue working
	b.Push(3)
	b.Push(4) // triggers another flush

	if cons.calls.Load() != 2 {
		t.Errorf("expected 2 flushes despite errors, got %d", cons.calls.Load())
	}
}

var errTest = &testError{}

type testError struct{}

func (e *testError) Error() string { return "test error" }

func TestFlush_SliceOwnership(t *testing.T) {
	// Verify consumer receives a copy, not the original slice
	cons := &mockConsumer[int]{}
	cap := 3
	b := New[int](cons, Config{StripeSize: cap})

	b.Push(1)
	b.Push(2)
	b.Push(3) // triggers flush

	if len(cons.batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(cons.batches))
	}

	// Modify the received batch
	cons.batches[0][0] = 999

	// Push more items and flush again
	b.Push(4)
	b.Push(5)
	b.Push(6)

	if len(cons.batches) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(cons.batches))
	}

	// Second batch should not be affected by modification to first
	if cons.batches[1][0] != 4 {
		t.Errorf("second batch corrupted, got %d want 4", cons.batches[1][0])
	}
}

// --- Concurrency Tests ---

func TestConcurrent_MultipleGoroutines(t *testing.T) {
	cons := &mockConsumer[int]{}
	cap := 100
	b := New[int](cons, Config{StripeSize: cap})

	numGoroutines := 10
	itemsPerGoroutine := 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				b.Push(offset*itemsPerGoroutine + i)
			}
		}(g)
	}

	wg.Wait()

	// Verify total items received
	// Note: Some items may still be in stripes (not flushed)
	// We can only verify that flushed items are correct
	totalPushed := numGoroutines * itemsPerGoroutine
	expectedFlushes := totalPushed / cap

	// Allow some variance due to sync.Pool behavior
	// Minimum expected flushes = floor(totalPushed / cap) - some tolerance
	minFlushes := expectedFlushes - numGoroutines
	if minFlushes < 0 {
		minFlushes = 0
	}

	actualFlushes := int(cons.calls.Load())
	if actualFlushes < minFlushes {
		t.Errorf("expected at least %d flushes, got %d", minFlushes, actualFlushes)
	}

	// Verify all flushed batches have correct size
	cons.mu.Lock()
	for i, batch := range cons.batches {
		if len(batch) != cap {
			t.Errorf("batch[%d] has size %d, expected %d", i, len(batch), cap)
		}
	}
	cons.mu.Unlock()
}

func TestConcurrent_HighContention(t *testing.T) {
	cons := &mockConsumer[int]{}
	b := New[int](cons, Config{StripeSize: 10})

	numGoroutines := 100
	itemsPerGoroutine := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				b.Push(i)
			}
		}()
	}

	wg.Wait()

	// No panics or data races = success
	// Verify some flushes happened
	if cons.calls.Load() == 0 {
		t.Error("expected at least some flushes")
	}
}

// --- Generic Type Tests ---

func TestGeneric_StringType(t *testing.T) {
	cons := &mockConsumer[string]{}
	b := New[string](cons, Config{StripeSize: 2})

	b.Push("hello")
	b.Push("world")

	if len(cons.batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(cons.batches))
	}

	if cons.batches[0][0] != "hello" || cons.batches[0][1] != "world" {
		t.Errorf("unexpected batch content: %v", cons.batches[0])
	}
}

func TestGeneric_StructType(t *testing.T) {
	type event struct {
		ID   int
		Name string
	}

	cons := &mockConsumer[event]{}
	b := New[event](cons, Config{StripeSize: 2})

	b.Push(event{ID: 1, Name: "event1"})
	b.Push(event{ID: 2, Name: "event2"})

	if len(cons.batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(cons.batches))
	}

	if cons.batches[0][0].ID != 1 || cons.batches[0][1].ID != 2 {
		t.Errorf("unexpected batch content: %v", cons.batches[0])
	}
}
