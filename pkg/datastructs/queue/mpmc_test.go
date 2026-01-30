package queue

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Interface compliance check
var _ Queue[int] = (*MPMC[int])(nil)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewMPMC(t *testing.T) {
	tests := []struct {
		name         string
		capacity     int
		wantCapacity uint64
	}{
		{"power_of_two", 16, 16},
		{"non_power_of_two_rounds_up", 100, 128},
		{"exact_power_of_two", 64, 64},
		{"small_power_of_two", 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewMPMC[int](tt.capacity)
			if q == nil {
				t.Fatal("NewMPMC returned nil")
			}
			if got := q.Capacity(); got != tt.wantCapacity {
				t.Errorf("Capacity() = %d, want %d", got, tt.wantCapacity)
			}
			if !q.IsEmpty() {
				t.Error("new queue should be empty")
			}
		})
	}
}

func TestNewMPMC_BoundaryCapacity(t *testing.T) {
	tests := []struct {
		name         string
		capacity     int
		wantCapacity uint64
	}{
		{"zero_uses_minimum", 0, 2},
		{"one_uses_minimum", 1, 2},
		{"negative_uses_minimum", -5, 2},
		{"negative_large_uses_minimum", -1000, 2},
		{"two_exact", 2, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewMPMC[int](tt.capacity)
			if got := q.Capacity(); got != tt.wantCapacity {
				t.Errorf("Capacity() = %d, want %d", got, tt.wantCapacity)
			}
		})
	}
}

// =============================================================================
// Enqueue Tests
// =============================================================================

func TestEnqueue(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		items    []int
		wantOk   []bool
	}{
		{
			name:     "single_item",
			capacity: 4,
			items:    []int{42},
			wantOk:   []bool{true},
		},
		{
			name:     "fill_to_capacity",
			capacity: 4,
			items:    []int{1, 2, 3, 4},
			wantOk:   []bool{true, true, true, true},
		},
		{
			name:     "exceed_capacity",
			capacity: 4,
			items:    []int{1, 2, 3, 4, 5},
			wantOk:   []bool{true, true, true, true, false},
		},
		{
			name:     "zero_value",
			capacity: 4,
			items:    []int{0, 0, 0},
			wantOk:   []bool{true, true, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewMPMC[int](tt.capacity)
			for i, item := range tt.items {
				got := q.Enqueue(item)
				if got != tt.wantOk[i] {
					t.Errorf("Enqueue(%d) = %v, want %v", item, got, tt.wantOk[i])
				}
			}
		})
	}
}

func TestEnqueue_AfterDequeue(t *testing.T) {
	q := NewMPMC[int](4)

	// Fill the queue
	for i := 1; i <= 4; i++ {
		q.Enqueue(i)
	}
	if !q.IsFull() {
		t.Error("queue should be full")
	}

	// Dequeue one item
	_, ok := q.Dequeue()
	if !ok {
		t.Error("Dequeue should succeed")
	}

	// Enqueue should now succeed (slot reused)
	if !q.Enqueue(5) {
		t.Error("Enqueue after Dequeue should succeed")
	}
}

func TestEnqueue_FillDrainRefill(t *testing.T) {
	q := NewMPMC[int](4)

	// Fill
	for i := 1; i <= 4; i++ {
		if !q.Enqueue(i) {
			t.Errorf("initial Enqueue(%d) failed", i)
		}
	}

	// Drain
	for i := 1; i <= 4; i++ {
		if _, ok := q.Dequeue(); !ok {
			t.Errorf("Dequeue %d failed", i)
		}
	}

	// Refill
	for i := 10; i <= 13; i++ {
		if !q.Enqueue(i) {
			t.Errorf("refill Enqueue(%d) failed", i)
		}
	}

	// Verify refilled values
	for i := 10; i <= 13; i++ {
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Errorf("Dequeue() = (%d, %v), want (%d, true)", v, ok, i)
		}
	}
}

// =============================================================================
// Dequeue Tests
// =============================================================================

func TestDequeue(t *testing.T) {
	t.Run("empty_queue", func(t *testing.T) {
		q := NewMPMC[int](4)
		v, ok := q.Dequeue()
		if ok {
			t.Error("Dequeue on empty queue should return false")
		}
		if v != 0 {
			t.Errorf("Dequeue on empty should return zero value, got %d", v)
		}
	})

	t.Run("single_item", func(t *testing.T) {
		q := NewMPMC[int](4)
		q.Enqueue(42)
		v, ok := q.Dequeue()
		if !ok || v != 42 {
			t.Errorf("Dequeue() = (%d, %v), want (42, true)", v, ok)
		}
	})

	t.Run("multiple_dequeues_on_empty", func(t *testing.T) {
		q := NewMPMC[int](4)
		for i := 0; i < 5; i++ {
			_, ok := q.Dequeue()
			if ok {
				t.Errorf("Dequeue %d on empty should return false", i)
			}
		}
	})
}

func TestDequeue_FIFOOrder(t *testing.T) {
	q := NewMPMC[int](8)
	items := []int{1, 2, 3, 4, 5}

	for _, item := range items {
		q.Enqueue(item)
	}

	for i, want := range items {
		got, ok := q.Dequeue()
		if !ok {
			t.Errorf("Dequeue %d failed", i)
		}
		if got != want {
			t.Errorf("Dequeue() = %d, want %d (FIFO order)", got, want)
		}
	}
}

func TestDequeue_ZeroValue(t *testing.T) {
	q := NewMPMC[int](4)

	// Enqueue zero values
	q.Enqueue(0)
	q.Enqueue(0)

	// Should successfully dequeue zero values
	for i := 0; i < 2; i++ {
		v, ok := q.Dequeue()
		if !ok {
			t.Errorf("Dequeue zero value %d should succeed", i)
		}
		if v != 0 {
			t.Errorf("Dequeue() = %d, want 0", v)
		}
	}

	// Now queue is empty
	_, ok := q.Dequeue()
	if ok {
		t.Error("Dequeue on empty should return false")
	}
}

// =============================================================================
// EnqueueBatch Tests
// =============================================================================

func TestEnqueueBatch(t *testing.T) {
	tests := []struct {
		name      string
		capacity  int
		items     []int
		wantCount int
	}{
		{"all_fit", 8, []int{1, 2, 3}, 3},
		{"partial_fit", 4, []int{1, 2, 3, 4, 5, 6}, 4},
		{"empty_slice", 4, []int{}, 0},
		{"nil_slice", 4, nil, 0},
		{"exact_capacity", 4, []int{1, 2, 3, 4}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewMPMC[int](tt.capacity)
			got := q.EnqueueBatch(tt.items)
			if got != tt.wantCount {
				t.Errorf("EnqueueBatch() = %d, want %d", got, tt.wantCount)
			}
		})
	}
}

func TestEnqueueBatch_PartiallyFull(t *testing.T) {
	q := NewMPMC[int](8)

	// Pre-fill with 3 items
	for i := 1; i <= 3; i++ {
		q.Enqueue(i)
	}

	// Batch enqueue 6 items, only 5 should fit
	items := []int{10, 11, 12, 13, 14, 15}
	count := q.EnqueueBatch(items)
	if count != 5 {
		t.Errorf("EnqueueBatch() = %d, want 5 (remaining capacity)", count)
	}
}

// =============================================================================
// DequeueBatch Tests
// =============================================================================

func TestDequeueBatch(t *testing.T) {
	tests := []struct {
		name       string
		enqueue    []int
		outSize    int
		wantCount  int
		wantValues []int
	}{
		{
			name:       "all_available",
			enqueue:    []int{1, 2, 3},
			outSize:    5,
			wantCount:  3,
			wantValues: []int{1, 2, 3},
		},
		{
			name:       "partial_available",
			enqueue:    []int{1, 2, 3, 4, 5},
			outSize:    3,
			wantCount:  3,
			wantValues: []int{1, 2, 3},
		},
		{
			name:       "empty_queue",
			enqueue:    []int{},
			outSize:    5,
			wantCount:  0,
			wantValues: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewMPMC[int](8)
			for _, v := range tt.enqueue {
				q.Enqueue(v)
			}

			out := make([]int, tt.outSize)
			got := q.DequeueBatch(out)
			if got != tt.wantCount {
				t.Errorf("DequeueBatch() = %d, want %d", got, tt.wantCount)
			}

			for i := 0; i < tt.wantCount; i++ {
				if out[i] != tt.wantValues[i] {
					t.Errorf("out[%d] = %d, want %d", i, out[i], tt.wantValues[i])
				}
			}
		})
	}
}

func TestDequeueBatch_NilSlice(t *testing.T) {
	q := NewMPMC[int](4)
	q.Enqueue(1)
	q.Enqueue(2)

	count := q.DequeueBatch(nil)
	if count != 0 {
		t.Errorf("DequeueBatch(nil) = %d, want 0", count)
	}
}

func TestDequeueBatch_FIFOOrder(t *testing.T) {
	q := NewMPMC[int](8)
	for i := 1; i <= 5; i++ {
		q.Enqueue(i)
	}

	out := make([]int, 5)
	q.DequeueBatch(out)

	for i, want := range []int{1, 2, 3, 4, 5} {
		if out[i] != want {
			t.Errorf("out[%d] = %d, want %d (FIFO)", out[i], i, want)
		}
	}
}

// =============================================================================
// Size Tests
// =============================================================================

func TestSize(t *testing.T) {
	q := NewMPMC[int](8)

	// Empty
	if s := q.Size(); s != 0 {
		t.Errorf("Size() on empty = %d, want 0", s)
	}

	// After enqueues
	for i := 1; i <= 3; i++ {
		q.Enqueue(i)
	}
	if s := q.Size(); s != 3 {
		t.Errorf("Size() after 3 enqueues = %d, want 3", s)
	}

	// After dequeue
	q.Dequeue()
	if s := q.Size(); s != 2 {
		t.Errorf("Size() after dequeue = %d, want 2", s)
	}

	// Full
	q2 := NewMPMC[int](4)
	for i := 1; i <= 4; i++ {
		q2.Enqueue(i)
	}
	if s := q2.Size(); s != 4 {
		t.Errorf("Size() when full = %d, want 4", s)
	}
}

// =============================================================================
// IsEmpty / IsFull Tests
// =============================================================================

func TestIsEmpty(t *testing.T) {
	q := NewMPMC[int](4)

	// New queue is empty
	if !q.IsEmpty() {
		t.Error("new queue should be empty")
	}

	// After enqueue, not empty
	q.Enqueue(1)
	if q.IsEmpty() {
		t.Error("queue with item should not be empty")
	}

	// After drain, empty again
	q.Dequeue()
	if !q.IsEmpty() {
		t.Error("drained queue should be empty")
	}
}

func TestIsFull(t *testing.T) {
	q := NewMPMC[int](4)

	// New queue is not full
	if q.IsFull() {
		t.Error("new queue should not be full")
	}

	// Fill to capacity
	for i := 1; i <= 4; i++ {
		q.Enqueue(i)
	}
	if !q.IsFull() {
		t.Error("queue at capacity should be full")
	}

	// After dequeue, not full
	q.Dequeue()
	if q.IsFull() {
		t.Error("queue below capacity should not be full")
	}
}

// =============================================================================
// Capacity Tests
// =============================================================================

func TestCapacity(t *testing.T) {
	tests := []struct {
		input int
		want  uint64
	}{
		{16, 16},
		{10, 16},
		{32, 32},
		{100, 128},
		{2, 2},
	}

	for _, tt := range tests {
		q := NewMPMC[int](tt.input)
		if got := q.Capacity(); got != tt.want {
			t.Errorf("NewMPMC(%d).Capacity() = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// =============================================================================
// Clear Tests
// =============================================================================

func TestClear(t *testing.T) {
	t.Run("with_items", func(t *testing.T) {
		q := NewMPMC[int](8)
		for i := 1; i <= 5; i++ {
			q.Enqueue(i)
		}
		q.Clear()
		if !q.IsEmpty() {
			t.Error("queue should be empty after Clear")
		}
		if s := q.Size(); s != 0 {
			t.Errorf("Size() after Clear = %d, want 0", s)
		}
	})

	t.Run("empty_queue", func(t *testing.T) {
		q := NewMPMC[int](8)
		q.Clear() // no-op
		if !q.IsEmpty() {
			t.Error("empty queue should remain empty after Clear")
		}
	})

	t.Run("enqueue_after_clear", func(t *testing.T) {
		q := NewMPMC[int](4)
		for i := 1; i <= 4; i++ {
			q.Enqueue(i)
		}
		q.Clear()

		// Should work normally after clear
		if !q.Enqueue(100) {
			t.Error("Enqueue after Clear should succeed")
		}
		v, ok := q.Dequeue()
		if !ok || v != 100 {
			t.Errorf("Dequeue() = (%d, %v), want (100, true)", v, ok)
		}
	})
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestConcurrency_MultiProducer(t *testing.T) {
	q := NewMPMC[int](1024)
	var wg sync.WaitGroup
	var enqueued atomic.Int64

	producers := 4
	itemsPerProducer := 200

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < itemsPerProducer; i++ {
				if q.Enqueue(id*1000 + i) {
					enqueued.Add(1)
				}
			}
		}(p)
	}

	wg.Wait()

	// All items should be enqueued (queue is large enough)
	expected := int64(producers * itemsPerProducer)
	if got := enqueued.Load(); got != expected {
		t.Errorf("enqueued %d items, want %d", got, expected)
	}

	if s := q.Size(); s != expected {
		t.Errorf("Size() = %d, want %d", s, expected)
	}
}

func TestConcurrency_MultiConsumer(t *testing.T) {
	q := NewMPMC[int](1024)

	// Pre-fill the queue
	totalItems := 800
	for i := 0; i < totalItems; i++ {
		q.Enqueue(i)
	}

	var wg sync.WaitGroup
	var dequeued atomic.Int64

	consumers := 4
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if _, ok := q.Dequeue(); ok {
					dequeued.Add(1)
				} else {
					// Check if queue is really empty
					if q.IsEmpty() {
						return
					}
				}
			}
		}()
	}

	wg.Wait()

	if got := dequeued.Load(); got != int64(totalItems) {
		t.Errorf("dequeued %d items, want %d", got, totalItems)
	}

	if !q.IsEmpty() {
		t.Errorf("queue should be empty, Size() = %d", q.Size())
	}
}

func TestConcurrency_MixedProducerConsumer(t *testing.T) {
	q := NewMPMC[int](256)

	var wg sync.WaitGroup
	var produced, consumed atomic.Int64

	producers := 2
	consumers := 2
	itemsPerProducer := 500

	// Start producers
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < itemsPerProducer; i++ {
				for !q.Enqueue(id*1000 + i) {
					// Retry until successful
				}
				produced.Add(1)
			}
		}(p)
	}

	// Start consumers
	done := make(chan struct{})
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					// Drain remaining
					for {
						if _, ok := q.Dequeue(); ok {
							consumed.Add(1)
						} else {
							return
						}
					}
				default:
					if _, ok := q.Dequeue(); ok {
						consumed.Add(1)
					}
				}
			}
		}()
	}

	// Wait for all producers to finish
	time.Sleep(100 * time.Millisecond)
	for produced.Load() < int64(producers*itemsPerProducer) {
		time.Sleep(10 * time.Millisecond)
	}

	close(done)
	wg.Wait()

	totalProduced := produced.Load()
	totalConsumed := consumed.Load()

	if totalProduced != int64(producers*itemsPerProducer) {
		t.Errorf("produced %d, want %d", totalProduced, producers*itemsPerProducer)
	}

	if totalConsumed != totalProduced {
		t.Errorf("consumed %d, produced %d - mismatch", totalConsumed, totalProduced)
	}
}

// =============================================================================
// Generic Type Tests
// =============================================================================

func TestMPMC_StringType(t *testing.T) {
	q := NewMPMC[string](4)

	q.Enqueue("hello")
	q.Enqueue("world")

	v1, ok1 := q.Dequeue()
	v2, ok2 := q.Dequeue()

	if !ok1 || v1 != "hello" {
		t.Errorf("first Dequeue = (%q, %v), want (hello, true)", v1, ok1)
	}
	if !ok2 || v2 != "world" {
		t.Errorf("second Dequeue = (%q, %v), want (world, true)", v2, ok2)
	}
}

func TestMPMC_StructType(t *testing.T) {
	type Item struct {
		ID   int
		Name string
	}

	q := NewMPMC[Item](4)

	q.Enqueue(Item{ID: 1, Name: "first"})
	q.Enqueue(Item{ID: 2, Name: "second"})

	v, ok := q.Dequeue()
	if !ok || v.ID != 1 || v.Name != "first" {
		t.Errorf("Dequeue = (%+v, %v), want ({ID:1 Name:first}, true)", v, ok)
	}
}

func TestMPMC_PointerType(t *testing.T) {
	q := NewMPMC[*int](4)

	val := 42
	q.Enqueue(&val)

	v, ok := q.Dequeue()
	if !ok || v == nil || *v != 42 {
		t.Errorf("Dequeue pointer failed")
	}

	// Nil pointer
	q.Enqueue(nil)
	v2, ok2 := q.Dequeue()
	if !ok2 || v2 != nil {
		t.Errorf("Dequeue nil pointer failed")
	}
}
