package workerpool

import "time"

// QueueType indicates the type of the worker queue.
type QueueType int

const (
	// QueueTypeFIFO indicates the use of a FIFO queue (First-In-First-Out).
	// This is the default mode, ensuring fair task distribution.
	QueueTypeFIFO QueueType = iota

	// QueueTypeLIFO indicates the use of a LIFO stack (Last-In-First-Out).
	// This mode can provide better CPU cache locality by reusing the most recently active workers.
	QueueTypeLIFO
)

// Queue is the interface for holding workers.
type Queue interface {
	len() int
	isEmpty() bool
	insert(Worker) error
	detach() Worker
	refresh(duration time.Duration) []Worker
	reset()
}

func newQueue(qType QueueType, size int) Queue {
	switch qType {
	case QueueTypeLIFO:
		return newLIFOQueue(size)
	case QueueTypeFIFO:
		return newFIFOQueue(size)
	default:
		return newFIFOQueue(size)
	}
}
