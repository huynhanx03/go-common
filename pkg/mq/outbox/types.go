package outbox

import (
	"context"
	"time"
)

// Attribute is bounded metadata propagated with a message.
type Attribute struct {
	Key   string
	Value string
}

// Metadata carries transport-neutral message metadata. Attributes are passed
// to Publisher but are never promoted into Relay's operational log fields.
type Metadata struct {
	CorrelationID string
	Attributes    []Attribute
}

// Message is the immutable value passed from Store to Publisher.
type Message struct {
	ID          string
	Destination string
	Key         []byte
	Payload     []byte
	Metadata    Metadata
}

// Lease is an opaque fencing capability issued by Store. Token values must
// never be logged or sent to Publisher.
type Lease struct {
	MessageID string
	Token     string
	Attempt   uint32
}

// ClaimedMessage binds a message to its current fencing lease.
type ClaimedMessage struct {
	Lease   Lease
	Message Message
}

// ClaimRequest bounds one atomic claim operation.
type ClaimRequest struct {
	Owner         string
	Limit         int
	LeaseDuration time.Duration
	// Destinations restricts the atomic claim to an explicit allowlist. An
	// empty list means every destination. Store implementations must not
	// return messages outside a non-empty list.
	Destinations []string
}

// PublishAck is optional publisher metadata retained by Store after success.
// Relay discards an unsafe or oversized Reference rather than turning a
// successful publish into a duplicate delivery.
type PublishAck struct {
	Reference string
}

// RetryDisposition describes a retry without exposing an underlying error.
type RetryDisposition struct {
	Delay     time.Duration
	Code      string
	Ambiguous bool
}

// DeadDisposition describes why a message reached a terminal state.
type DeadDisposition struct {
	Code      string
	Ambiguous bool
}

// Store owns durable message state, claiming, and fencing.
//
// Claim must atomically select messages in deterministic order, return at most
// Limit unique message identities, issue a new opaque token, and increment the
// one-based Attempt for every issued lease. A non-nil error must return no
// claimed messages.
// Implementations should use their storage engine's clock for leases and retry
// availability. Mark methods must fence on both message identity and token and
// return ErrLeaseLost for a stale token. A current token remains finalizable
// after its wall-clock lease deadline until another claim replaces it. Dead
// messages are retained for inspection rather than deleted implicitly.
//
// Returning more than ClaimRequest.Limit or a message outside a non-empty
// ClaimRequest.Destinations allowlist is a fatal ErrStoreContract condition;
// the relay drains already admitted work and lets leases from the violating
// claim expire. Every method must honor context cancellation and may be called
// concurrently.
type Store interface {
	Claim(context.Context, ClaimRequest) ([]ClaimedMessage, error)
	MarkPublished(context.Context, Lease, PublishAck) error
	MarkRetry(context.Context, Lease, RetryDisposition) error
	MarkDead(context.Context, Lease, DeadDisposition) error
}

// Publisher publishes one relay-owned message. Implementations must honor
// context cancellation, return only after the publish outcome is known, and
// should use Message.ID as their idempotency key when the destination supports
// one. Mutating the value cannot affect Store-owned memory, but implementations
// should treat it as immutable.
type Publisher interface {
	Publish(context.Context, Message) (PublishAck, error)
}

// PublisherFunc adapts a function to Publisher.
type PublisherFunc func(context.Context, Message) (PublishAck, error)

// Publish delegates to the function.
func (function PublisherFunc) Publish(ctx context.Context, message Message) (PublishAck, error) {
	return function(ctx, message)
}

// Wakeup is an optional latency hint. Correctness must not depend on receiving
// a wakeup because Relay always retains a polling fallback. Wait must block
// until a hint, error, or context cancellation and must honor cancellation.
// Relay does not wait indefinitely for a broken implementation during shutdown;
// ignoring cancellation can therefore leak the implementation's Wait goroutine.
type Wakeup interface {
	Wait(context.Context) error
}

// WakeupFunc adapts a function to Wakeup.
type WakeupFunc func(context.Context) error

// Wait delegates to the function.
func (function WakeupFunc) Wait(ctx context.Context) error { return function(ctx) }

// Backoff calculates retry delay from the one-based delivery attempt.
type Backoff interface {
	Delay(attempt uint32) time.Duration
}

// BackoffFunc adapts a function to Backoff.
type BackoffFunc func(attempt uint32) time.Duration

// Delay delegates to the function.
func (function BackoffFunc) Delay(attempt uint32) time.Duration { return function(attempt) }

// PublishError classifies publisher failures without coupling Store to a
// transport-specific error type.
type PublishError struct {
	Code      string
	Retryable bool
	Ambiguous bool
	Cause     error
}

func (failure *PublishError) Error() string {
	if failure == nil {
		return "outbox: publish failed"
	}
	if validBoundedText(failure.Code, maximumAttributeKeyBytes) {
		return "outbox: publish failed: " + failure.Code
	}
	return "outbox: publish failed"
}

// Unwrap preserves the transport error for errors.Is and errors.As.
func (failure *PublishError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}
