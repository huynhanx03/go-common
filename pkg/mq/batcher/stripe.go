package batcher

// stripe represents a single buffer stripe.
// It is NOT thread-safe and is intended to be used via sync.Pool.
type stripe[T any] struct {
	cons Consumer[T]
	data []T
	cap  int
}

// newStripe creates a new stripe with the given consumer and capacity.
func newStripe[T any](cons Consumer[T], capacity int) *stripe[T] {
	return &stripe[T]{
		cons: cons,
		data: make([]T, 0, capacity),
		cap:  capacity,
	}
}

// Push appends an item to the stripe.
// If the stripe becomes full, it flushes data to the consumer.
func (s *stripe[T]) Push(item T) {
	s.data = append(s.data, item)

	if len(s.data) >= s.cap {
		// Flush to consumer
		// Note: We ignore error here as this is a fire-and-forget pattern typically.
		// Real error handling should be done inside the Consumer implementation.
		_ = s.cons.Consume(s.data)

		// Allocation strategy:
		// We allocate a new slice to ensure the Consumer owns the passed data safely.
		// This matches Ristretto's safety guarantee.
		s.data = make([]T, 0, s.cap)
	}
}
