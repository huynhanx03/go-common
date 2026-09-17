package outbox

import (
	"errors"
	"testing"
	"unsafe"
)

func TestPublishErrorIsBoundedAndPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("cause")
	failure := &PublishError{Code: "unavailable", Cause: cause}
	if got := failure.Error(); got != "outbox: publish failed: unavailable" {
		t.Fatalf("Error = %q", got)
	}
	if !errors.Is(failure, cause) {
		t.Fatal("PublishError does not preserve Cause")
	}
	unsafe := &PublishError{Code: "bad\ncode"}
	if got := unsafe.Error(); got != "outbox: publish failed" {
		t.Fatalf("unsafe Error = %q, want generic message", got)
	}
	var nilFailure *PublishError
	if nilFailure.Error() != "outbox: publish failed" || nilFailure.Unwrap() != nil {
		t.Fatal("nil PublishError methods are not total")
	}
}

func TestClaimAndPublishAckCloneBoundedStringsAtTrustBoundary(t *testing.T) {
	t.Parallel()

	backing := []byte("message-1|events|cid-1|key|value|token|reference")
	alias := func(start, end int) string {
		return unsafe.String(unsafe.SliceData(backing[start:end]), end-start)
	}
	claimed := ClaimedMessage{
		Lease: Lease{MessageID: alias(0, 9), Token: alias(33, 38), Attempt: 1},
		Message: Message{
			ID: alias(0, 9), Destination: alias(10, 16),
			Metadata: Metadata{
				CorrelationID: alias(17, 22),
				Attributes:    []Attribute{{Key: alias(23, 26), Value: alias(27, 32)}},
			},
		},
	}
	owned := cloneClaimedMessage(claimed)
	ack := normalizedPublishAck(PublishAck{Reference: alias(39, len(backing))})
	for index := range backing {
		backing[index] = 'x'
	}
	if owned.Lease.MessageID != "message-1" || owned.Lease.Token != "token" ||
		owned.Message.ID != "message-1" || owned.Message.Destination != "events" ||
		owned.Message.Metadata.CorrelationID != "cid-1" ||
		owned.Message.Metadata.Attributes[0] != (Attribute{Key: "key", Value: "value"}) ||
		ack.Reference != "reference" {
		t.Fatalf("owned values retained caller backing storage: owned=%+v ack=%+v", owned, ack)
	}
}

func TestInvalidAttributeBombIsRejectedBeforeCloning(t *testing.T) {
	message := validMessage("message-1")
	message.Metadata.Attributes = make([]Attribute, 1<<20)
	lease := Lease{MessageID: message.ID, Token: "token", Attempt: 1}
	if _, err := normalizedMessage(lease, message, validOptions().MaxMessageBytes); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("normalizedMessage error = %v, want ErrInvalidMessage", err)
	}
	allocations := testing.AllocsPerRun(10, func() {
		normalizedMessageSink, normalizedMessageErrorSink = normalizedMessage(
			lease, message, validOptions().MaxMessageBytes,
		)
	})
	if allocations != 0 {
		t.Fatalf("invalid metadata allocations = %.1f, want 0 before ownership clone", allocations)
	}
}

var (
	normalizedMessageSink      Message
	normalizedMessageErrorSink error
)

func TestOpaqueMessageBodiesUseCallerConfiguredSizeBudget(t *testing.T) {
	t.Parallel()

	message := validMessage("message-1")
	message.Key = make([]byte, 1024*1024+1)
	message.Payload = make([]byte, 16*1024*1024+1)
	_, err := normalizedMessage(
		Lease{MessageID: message.ID, Token: "token", Attempt: 1},
		message,
		len(message.Key)+len(message.Payload),
	)
	if err != nil {
		t.Fatalf("normalizedMessage rejected opaque bodies: %v", err)
	}
}
