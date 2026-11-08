// Package consumer processes messages independently of their transport.
package consumer

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/huynhanx03/go-common/pkg/logger"
)

// Outcome tells a transport how to settle a message after its handler returns.
type Outcome uint8

const (
	// Success acknowledges the message after processing completes.
	Success Outcome = iota + 1
	// Retry leaves the message unacknowledged for redelivery.
	Retry
	// DeadLetter sends the message to the transport's dead-letter handler.
	DeadLetter
)

// Header is one message header. A slice preserves duplicate header names.
type Header struct {
	Key   string
	Value []byte
}

// IDFromHeaders returns a valid producer-assigned message ID. The canonical
// header takes precedence over the existing Forge outbox header.
func IDFromHeaders(headers []Header) string {
	var outboxID string
	for _, header := range headers {
		if header.Key != MessageIDHeader && header.Key != OutboxMessageIDHeader {
			continue
		}
		id := string(header.Value)
		if !IsValidMessageID(id) {
			continue
		}
		switch header.Key {
		case MessageIDHeader:
			return id
		case OutboxMessageIDHeader:
			if outboxID == "" {
				outboxID = id
			}
		}
	}
	return outboxID
}

// IsValidMessageID reports whether id is safe to use as a text deduplication key.
func IsValidMessageID(id string) bool {
	return id != "" && strings.TrimSpace(id) == id && utf8.ValidString(id) && !strings.ContainsRune(id, '\x00')
}

// Message contains the payload and metadata shared by supported transports.
// ID is producer-stable when provided, otherwise it identifies the broker
// record. Handlers should not retain its byte slices after returning.
type Message struct {
	ID        string
	Topic     string
	Stream    string // Source partition; messages in one stream run in order by default.
	Key       []byte
	Value     []byte
	Headers   []Header
	Timestamp time.Time
}

// Handler returns the processing outcome and an optional cause.
type Handler func(context.Context, Message) (Outcome, error)

// Result is the processing outcome for one input message.
type Result struct {
	Outcome Outcome
	Err     error
}

// BatchHandler returns one result per message, in input order. It transfers
// ownership of the result slice to the runtime.
type BatchHandler func(context.Context, []Message) []Result

// Stats describes work currently owned by the runtime.
type Stats struct {
	Queued  int
	Running int
}

// MessageContext restores correlation for one message. A batch spans multiple
// requests; use this for per-message logs inside a BatchHandler.
func MessageContext(ctx context.Context, message Message) context.Context {
	for _, header := range message.Headers {
		if header.Key == logger.RequestIDHeader {
			return logger.WithRequestID(ctx, string(header.Value))
		}
	}
	return ctx
}

// DeadLetterHandler must durably publish the message before returning nil.
type DeadLetterHandler func(context.Context, Message, error) error

// Middleware wraps a shared message handler.
type Middleware func(Handler) Handler

// Chain applies middleware from outermost to innermost in the listed order.
func Chain(handler Handler, middlewares ...Middleware) Handler {
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	return handler
}

// Recovery converts a handler panic into a retry outcome with its stack trace.
func Recovery(next Handler) Handler {
	return func(ctx context.Context, message Message) (outcome Outcome, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				outcome = Retry
				err = fmt.Errorf("%w: %v\n%s", ErrHandlerPanic, recovered, debug.Stack())
			}
		}()
		return next(ctx, message)
	}
}
