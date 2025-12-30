package workerpool

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/algorithm"
)

var _ Queue = (*fifoQueue)(nil)

type fifoQueue struct {
	items  []Worker
	expiry []Worker
	head   int
	tail   int
	size   int
	isFull bool
}

func newFIFOQueue(size int) *fifoQueue {
	if size <= 0 {
		return nil
	}
	return &fifoQueue{
		items: make([]Worker, size),
		size:  size,
	}
}

// len returns the number of workers in the queue.
func (wq *fifoQueue) len() int {
	if wq.size == 0 || wq.isEmpty() {
		return 0
	}

	if wq.head == wq.tail && wq.isFull {
		return wq.size
	}

	if wq.tail > wq.head {
		return wq.tail - wq.head
	}

	return wq.size - wq.head + wq.tail
}

// isEmpty returns true if the queue is empty.
func (wq *fifoQueue) isEmpty() bool {
	return wq.head == wq.tail && !wq.isFull
}

// insert inserts a worker into the queue.
func (wq *fifoQueue) insert(w Worker) error {
	if wq.size == 0 {
		return nil
	}
	if wq.isFull {
		return ErrQueueIsFull
	}
	wq.items[wq.tail] = w
	wq.tail = (wq.tail + 1) % wq.size

	if wq.tail == wq.head {
		wq.isFull = true
	}

	return nil
}

// detach removes and returns the worker at the head of the queue.
func (wq *fifoQueue) detach() Worker {
	if wq.isEmpty() {
		return nil
	}

	w := wq.items[wq.head]
	wq.items[wq.head] = nil
	wq.head = (wq.head + 1) % wq.size

	wq.isFull = false

	return w
}

// refresh retrieves and removes all expired workers from the queue.
// Since the queue is a circular buffer sorted by time (oldest at head),
// we identify the range of expired workers and extract them.
// The range might wrap around the buffer, requiring segmented extraction.
func (wq *fifoQueue) refresh(duration time.Duration) []Worker {
	expiryTime := time.Now().Add(-duration)
	index := wq.indexExpired(expiryTime)
	if index == -1 {
		return nil
	}
	wq.expiry = wq.expiry[:0]

	if wq.head <= index {
		// No wrap-around: expired workers are in a contiguous block [head, index]
		wq.expiry = append(wq.expiry, wq.items[wq.head:index+1]...)
		for i := wq.head; i < index+1; i++ {
			wq.items[i] = nil
		}
	} else {
		// Wrap-around: expired workers are in [head, end] and [0, index]
		wq.expiry = append(wq.expiry, wq.items[wq.head:]...)
		wq.expiry = append(wq.expiry, wq.items[0:index+1]...)
		for i := 0; i < index+1; i++ {
			wq.items[i] = nil
		}
		for i := wq.head; i < wq.size; i++ {
			wq.items[i] = nil
		}
	}
	head := (index + 1) % wq.size
	wq.head = head
	if len(wq.expiry) > 0 {
		wq.isFull = false
	}

	return wq.expiry
}

// indexExpired uses binary search to find the index of the last expired worker.
// The queue is ordered by time (head is oldest). We search for the first valid (non-expired) worker.
// The worker immediately preceding the first valid worker is the last expired one.
func (wq *fifoQueue) indexExpired(expiryTime time.Time) int {
	if wq.isEmpty() || expiryTime.Before(wq.items[wq.head].lastUsedTime()) {
		return -1
	}

	nlen := len(wq.items)

	// BinarySearch finds the first index i where function returns true (i.e., worker is valid).
	firstValid := algorithm.BinarySearch(0, wq.len()-1, func(i int) bool {
		pi := (wq.head + i) % nlen
		return expiryTime.Before(wq.items[pi].lastUsedTime())
	})

	if firstValid == 0 {
		return -1
	}

	// firstValid - 1 is the last expired worker relative to head.
	return (wq.head + (firstValid - 1)) % nlen
}

// reset resets the queue.
func (wq *fifoQueue) reset() {
	if wq.isEmpty() {
		return
	}

retry:
	if w := wq.detach(); w != nil {
		w.finish()
		goto retry
	}
	wq.head = 0
	wq.tail = 0
}
