package workerpool

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/algorithm"
)

var _ Queue = (*lifoQueue)(nil)

type lifoQueue struct {
	items  []Worker
	expiry []Worker
}

func newLIFOQueue(size int) *lifoQueue {
	return &lifoQueue{
		items: make([]Worker, 0, size),
	}
}

// len returns the number of workers in the queue.
func (ws *lifoQueue) len() int {
	return len(ws.items)
}

// isEmpty returns true if the queue is empty.
func (ws *lifoQueue) isEmpty() bool {
	return len(ws.items) == 0
}

// insert inserts a worker into the queue.
func (ws *lifoQueue) insert(w Worker) error {
	ws.items = append(ws.items, w)
	return nil
}

// detach removes and returns the worker at the end of the queue.
func (ws *lifoQueue) detach() Worker {
	l := ws.len()
	if l == 0 {
		return nil
	}

	w := ws.items[l-1]
	ws.items[l-1] = nil // avoid memory leaks
	ws.items = ws.items[:l-1]

	return w
}

// refresh retrieves and removes all expired workers.
// In a LIFO stack, workers are appended to the end, so the oldest workers (lowest lastUsedTime)
// are at the beginning (index 0).
// We binary search for the first non-expired worker. All workers before this index are expired.
// We then shifts the remaining valid workers down to the start of the slice.
func (ws *lifoQueue) refresh(duration time.Duration) []Worker {
	n := ws.len()
	if n == 0 {
		return nil
	}

	expiryTime := time.Now().Add(-duration)

	// Find the index of the first valid (non-expired) worker.
	// Since items are sorted by time (oldest at 0), this gives us the split point.
	index := algorithm.BinarySearch(0, n-1, func(i int) bool {
		return expiryTime.Before(ws.items[i].lastUsedTime())
	})

	lastExpiredIndex := index - 1
	if lastExpiredIndex < 0 {
		return nil
	}

	ws.expiry = ws.expiry[:0]
	ws.expiry = append(ws.expiry, ws.items[:lastExpiredIndex+1]...)

	// Shift remaining valid workers to the beginning of the slice
	copy(ws.items, ws.items[lastExpiredIndex+1:])

	// Release references to the checked-out workers to avoid memory leaks
	newLen := n - (lastExpiredIndex + 1)
	for i := newLen; i < n; i++ {
		ws.items[i] = nil
	}
	ws.items = ws.items[:newLen]

	return ws.expiry
}

// reset resets the queue.
func (ws *lifoQueue) reset() {
	for i := 0; i < ws.len(); i++ {
		ws.items[i].finish()
		ws.items[i] = nil
	}
	ws.items = ws.items[:0]
}
