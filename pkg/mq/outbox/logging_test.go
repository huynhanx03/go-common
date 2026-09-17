package outbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/correlation"
	"github.com/huynhanx03/go-common/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRelayLogsLifecycleAtInfoAndPerMessageSuccessAtDebug(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.DebugLevel)
	ctx, cancel := context.WithCancel(logger.WithContext(context.Background(), zap.New(core)))
	defer cancel()
	var claimed atomic.Bool
	message := validMessage("message-1")
	message.Metadata.Attributes = []Attribute{
		{Key: "failure_code", Value: "spoofed"},
		{Key: "custom", Value: "not-a-log-field"},
	}
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease:   Lease{MessageID: "message-1", Token: "token", Attempt: 1},
				Message: message,
			}}, nil
		},
		markPublished: func(context.Context, Lease, PublishAck) error {
			cancel()
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, message := range []string{"outbox relay started", "outbox relay stopped"} {
		entries := observed.FilterMessage(message).All()
		if len(entries) != 1 || entries[0].Level != zap.InfoLevel {
			t.Fatalf("%q entries = %+v, want one info entry", message, entries)
		}
	}
	published := observed.FilterMessage("outbox message published").All()
	if len(published) != 1 || published[0].Level != zap.DebugLevel {
		t.Fatalf("published entries = %+v, want one debug entry", published)
	}
	for _, key := range []string{"failure_code", "custom"} {
		if _, exists := published[0].ContextMap()[key]; exists {
			t.Fatalf("publisher metadata key %q leaked into operational logs", key)
		}
	}
}

func TestRelayBackoffWarningRetainsMessageCorrelationAndDeliveryFields(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	ctx = correlation.WithContext(ctx, "process-cid")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var claimed atomic.Bool
	message := validMessage("message-1")
	message.Metadata.CorrelationID = "event-cid"
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease:   Lease{MessageID: message.ID, Token: "opaque", Attempt: 1},
				Message: message,
			}}, nil
		},
		markRetry: func(context.Context, Lease, RetryDisposition) error {
			cancel()
			return nil
		},
	}
	options := validOptions()
	options.Backoff = BackoffFunc(func(uint32) time.Duration { return -1 })
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{}, &PublishError{
			Code: "provider_unavailable", Retryable: true, Ambiguous: true,
		}
	}), nil, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := observed.FilterMessage("outbox backoff returned an invalid delay").All()
	if len(entries) != 1 {
		t.Fatalf("invalid backoff entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	for key, want := range map[string]any{
		"correlation_id": "event-cid", "message_id": "message-1",
		"destination": "events", "attempt": "1",
	} {
		if got := fields[key]; got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
}
