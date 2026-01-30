package queue

import (
	"math/bits"
	"runtime"
	"sync/atomic"

	pkgRuntime "github.com/huynhanx03/go-common/pkg/runtime"
	"github.com/huynhanx03/go-common/pkg/utils"
)

var _ Queue[int] = (*MPMC[int])(nil)

const (
	cacheLineSize = 64

	// Spinning constants for Adaptive Spinning strategy.
	// Active spin: use PAUSE instruction (low power, keeps CPU warm).
	// Passive spin: yield to scheduler.
	activeSpinCycles = 4  // Number of PAUSE cycles per active spin iteration
	activeSpinTries  = 30 // Max active spin iterations before yielding
)

type slot[T any] struct {
	turn atomic.Uint64            // Turn number for producer/consumer
	data T                        // Data stored in the slot
	_    [cacheLineSize - 16]byte // Padding to prevent false sharing
}

// MPMC is a lock-free bounded multiple-producer multiple-consumer queue.
type MPMC[T any] struct {
	capacity     uint64    // Maximum capacity of the queue
	mask         uint64    // Mask for fast modulo
	capacityLog2 uint64    // Log2 of capacity for fast division
	slots        []slot[T] // Array of slots

	_ [cacheLineSize]byte // Padding to prevent false sharing

	head atomic.Uint64 // Head position

	_ [cacheLineSize]byte // Padding to prevent false sharing

	tail atomic.Uint64 // Tail position

	// _ [cacheLineSize]byte // Padding to prevent false sharing
}

// NewMPMC creates a queue with capacity rounded up to power of 2.
func NewMPMC[T any](capacity int) *MPMC[T] {
	if capacity < 2 {
		capacity = 2
	}
	capacity = utils.CeilToPowerOfTwo(capacity)

	q := &MPMC[T]{
		capacity:     uint64(capacity),
		mask:         uint64(capacity - 1),
		capacityLog2: uint64(bits.TrailingZeros64(uint64(capacity))),
		slots:        make([]slot[T], capacity),
	}

	for i := 0; i < capacity; i++ {
		q.slots[i].turn.Store(0)
	}

	return q
}

func (q *MPMC[T]) idx(pos uint64) uint64  { return pos & q.mask }
func (q *MPMC[T]) turn(pos uint64) uint64 { return pos >> q.capacityLog2 }

// Enqueue adds an item. Returns false if queue is full.
func (q *MPMC[T]) Enqueue(item T) bool {
	for spin := 0; ; spin++ {
		head := q.head.Load()
		idx := q.idx(head)
		expectedTurn := q.turn(head) * 2

		if q.slots[idx].turn.Load() == expectedTurn {
			if q.head.CompareAndSwap(head, head+1) {
				q.slots[idx].data = item
				q.slots[idx].turn.Store(expectedTurn + 1)
				return true
			}
		} else {
			if head == q.head.Load() {
				return false
			}
		}

		if spin < activeSpinTries {
			pkgRuntime.Procyield(activeSpinCycles)
		} else {
			runtime.Gosched()
			spin = 0
		}
	}
}

// Dequeue removes and returns an item. Returns false if queue is empty.
func (q *MPMC[T]) Dequeue() (T, bool) {
	var zero T

	for spin := 0; ; spin++ {
		tail := q.tail.Load()
		idx := q.idx(tail)
		expectedTurn := q.turn(tail)*2 + 1

		if q.slots[idx].turn.Load() == expectedTurn {
			if q.tail.CompareAndSwap(tail, tail+1) {
				data := q.slots[idx].data
				q.slots[idx].data = zero
				q.slots[idx].turn.Store(expectedTurn + 1)
				return data, true
			}
		} else {
			if tail == q.tail.Load() {
				return zero, false
			}
		}

		// Adaptive Spinning: Active spin first, then yield.
		if spin < activeSpinTries {
			pkgRuntime.Procyield(activeSpinCycles)
		} else {
			runtime.Gosched()
			spin = 0
		}
	}
}

// EnqueueBatch adds multiple items. Returns count of items enqueued.
func (q *MPMC[T]) EnqueueBatch(items []T) int {
	count := 0
	for _, item := range items {
		if !q.Enqueue(item) {
			break
		}
		count++
	}
	return count
}

// DequeueBatch removes multiple items into out slice. Returns count dequeued.
func (q *MPMC[T]) DequeueBatch(out []T) int {
	count := 0
	for i := range out {
		item, ok := q.Dequeue()
		if !ok {
			break
		}
		out[i] = item
		count++
	}
	return count
}

// Size returns approximate item count (may be negative during concurrent access).
func (q *MPMC[T]) Size() int64 {
	return int64(q.head.Load()) - int64(q.tail.Load())
}

// IsEmpty returns true if queue appears empty.
func (q *MPMC[T]) IsEmpty() bool { return q.Size() <= 0 }

// IsFull returns true if queue appears full.
func (q *MPMC[T]) IsFull() bool { return q.Size() >= int64(q.capacity) }

// Capacity returns maximum queue size.
func (q *MPMC[T]) Capacity() uint64 { return q.capacity }

// Clear drains all items from the queue.
func (q *MPMC[T]) Clear() {
	for {
		if _, ok := q.Dequeue(); !ok {
			break
		}
	}
}
