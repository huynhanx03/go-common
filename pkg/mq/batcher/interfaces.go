package batcher

// Consumer is the interface that must be implemented by users of the Batcher.
// It is responsible for processing a batch of items.
type Consumer[T any] interface {
	// Consume processes a batch of items.
	// Returns an error if processing fails.
	Consume(batch []T) error
}

// Config holds configuration for the StripedBatcher.
type Config struct {
	// StripeSize is the capacity of a single stripe buffer.
	// When a stripe reaches this size, it will be flushed to the Consumer.
	StripeSize int
}
