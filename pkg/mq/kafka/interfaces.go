package kafka

import (
	"context"
)

// Producer defines the contract for async message publishing
type Producer interface {
	Publish(ctx context.Context, topic string, key, value []byte)
	Errors() <-chan error
	Close() error
}

// SyncProducer defines the contract for reliable message publishing
type SyncProducer interface {
	Publish(ctx context.Context, topic string, key, value []byte) (partition int32, offset int64, err error)
	Close() error
}

// ConsumerGroup defines the contract for consuming messages
type ConsumerGroup interface {
	Start(ctx context.Context, topics []string, handler Handler, errHandler ErrorHandler) error
	Close() error
}

// Handler processed messages
type Handler func(ctx context.Context, key, value []byte) error

// ErrorHandler handles internal errors (e.g. from consumer loop or async producer)
type ErrorHandler func(err error)

// Middleware wraps a Handler to add functionality (logging, recovery, etc.)
type Middleware func(Handler) Handler
