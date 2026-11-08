package kafka

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

// Producer defines the contract for async message publishing
type Producer interface {
	Publish(ctx context.Context, topic string, key, value []byte)
	Errors() <-chan error
	Close() error
}

// ProducerWithID can attach a stable logical message ID for consumer deduplication.
type ProducerWithID interface {
	Producer
	PublishWithID(ctx context.Context, topic, messageID string, key, value []byte)
}

// SyncProducer defines the contract for reliable message publishing
type SyncProducer interface {
	Publish(ctx context.Context, topic string, key, value []byte) (partition int32, offset int64, err error)
	Close() error
}

// SyncProducerWithID is a synchronous producer that supports stable message IDs.
type SyncProducerWithID interface {
	SyncProducer
	PublishWithID(ctx context.Context, topic, messageID string, key, value []byte) (partition int32, offset int64, err error)
}

// ConsumerGroup defines the contract for consuming messages
type ConsumerGroup interface {
	Start(ctx context.Context, topics []string, handler Handler, errHandler ErrorHandler) error
	Close() error
}

// ResultConsumerGroup supports explicit success, retry, and dead-letter
// outcomes while retaining the original ConsumerGroup API.
type ResultConsumerGroup interface {
	ConsumerGroup
	StartWithRuntime(context.Context, []string, *consumer.Runtime, ErrorHandler, consumer.DeadLetterHandler) error
	StartWithResult(
		ctx context.Context,
		topics []string,
		handler consumer.Handler,
		errHandler ErrorHandler,
		deadLetterHandler consumer.DeadLetterHandler,
		middlewares ...consumer.Middleware,
	) error
}

// Handler processed messages
type Handler func(ctx context.Context, key, value []byte) error

// ErrorHandler handles internal errors (e.g. from consumer loop or async producer)
type ErrorHandler func(err error)

// Middleware wraps a Handler to add functionality (logging, recovery, etc.)
type Middleware func(Handler) Handler
