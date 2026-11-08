package consumer

import "errors"

var (
	ErrInvalidConfig          = errors.New("invalid consumer configuration")
	ErrClosed                 = errors.New("consumer is closed")
	ErrQueueFull              = errors.New("consumer queue is full")
	ErrHandlerPanic           = errors.New("message handler panicked")
	ErrBatchResultCount       = errors.New("batch result count does not match message count")
	ErrRetryRequested         = errors.New("message retry requested")
	ErrPrecedingMessageFailed = errors.New("preceding message in stream needs retry")
	// ErrInvalidOutcome marks a handler result that has no defined disposition.
	ErrInvalidOutcome = errors.New("invalid consumer outcome")
	// ErrDeadLetterHandlerRequired marks a terminal result without a durable sink.
	ErrDeadLetterHandlerRequired = errors.New("dead-letter handler is required")
)
