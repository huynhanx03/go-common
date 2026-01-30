package queue

// Queue is a generic interface for FIFO queues.
type Queue[T any] interface {
	// Enqueue adds an item to the queue.
	// Returns true if successful, false if the queue is full.
	Enqueue(item T) bool

	// Dequeue removes and returns an item from the queue.
	// Returns (item, true) if successful, (zero, false) if the queue is empty.
	Dequeue() (T, bool)

	// Capacity returns the total capacity of the queue.
	Capacity() uint64
}
