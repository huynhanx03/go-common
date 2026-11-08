package outbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRelayLogsLifecycleAtInfoAndPerMessageSuccessAtDebug(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.DebugLevel)
	ctx, cancel := context.WithCancel(context.Background())
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
	relay, err := New(logger.Wrap(zap.New(core)), store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
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
	ctx := context.Background()
	ctx = logger.WithRequestID(ctx, "process-request")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var claimed atomic.Bool
	message := validMessage("message-1")
	message.Metadata.CorrelationID = "event-correlation"
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
	relay, err := New(logger.Wrap(zap.New(core)), store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
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
		"request_id": "process-request", "correlation_id": "event-correlation", "message_id": "message-1",
		"destination": "events", "attempt": "1",
	} {
		if got := fields[key]; got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
}

func TestRelayReportsFinalizationFailuresWithoutPublishingAgain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		publishError  error
		storeError    error
		storePanic    bool
		wantOperation string
		wantCode      string
	}{
		{
			name:          "published marker timeout",
			storeError:    context.DeadlineExceeded,
			wantOperation: "mark_published",
			wantCode:      "store_timeout",
		},
		{
			name:          "retry marker lost lease",
			publishError:  &PublishError{Code: "unavailable", Retryable: true},
			storeError:    ErrLeaseLost,
			wantOperation: "mark_retry",
			wantCode:      "lease_lost",
		},
		{
			name:          "dead marker canceled",
			publishError:  &PublishError{Code: "rejected", Retryable: false},
			storeError:    context.Canceled,
			wantOperation: "mark_dead",
			wantCode:      "store_canceled",
		},
		{
			name:          "dead marker panic is contained",
			publishError:  &PublishError{Code: "rejected", Retryable: false},
			storePanic:    true,
			wantOperation: "mark_dead",
			wantCode:      failureStore,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			core, observed := observer.New(zap.DebugLevel)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var claimCount atomic.Int32
			var publishCount atomic.Int32
			var finalizeCount atomic.Int32
			finish := func() error {
				finalizeCount.Add(1)
				cancel()
				if test.storePanic {
					panic("store implementation panic")
				}
				return test.storeError
			}
			store := storeFuncs{
				claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
					if claimCount.Add(1) > 1 {
						cancel()
						return nil, nil
					}
					message := validMessage("message-1")
					return []ClaimedMessage{{
						Lease:   Lease{MessageID: message.ID, Token: "lease-token", Attempt: 1},
						Message: message,
					}}, nil
				},
				markPublished: func(context.Context, Lease, PublishAck) error { return finish() },
				markRetry:     func(context.Context, Lease, RetryDisposition) error { return finish() },
				markDead:      func(context.Context, Lease, DeadDisposition) error { return finish() },
			}
			relay, err := New(logger.Wrap(zap.New(core)), store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
				publishCount.Add(1)
				return PublishAck{}, test.publishError
			}), nil, func() Options {
				options := validOptions()
				options.Concurrency = 1
				options.ClaimLimit = 1
				return options
			}())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := relay.Run(ctx); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := publishCount.Load(); got != 1 {
				t.Fatalf("publish count = %d, want one attempt", got)
			}
			if got := finalizeCount.Load(); got != 1 {
				t.Fatalf("finalization count = %d, want one attempt", got)
			}

			entries := observed.FilterMessage("outbox store operation failed").All()
			if len(entries) != 1 {
				t.Fatalf("store failure log count = %d, want one", len(entries))
			}
			fields := entries[0].ContextMap()
			if fields["operation"] != test.wantOperation || fields["failure_code"] != test.wantCode {
				t.Fatalf("store failure fields = %v, want operation=%q failure_code=%q", fields, test.wantOperation, test.wantCode)
			}
		})
	}
}

func TestRelayContainsClaimStorePanicAndContinuesUntilCanceled(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.DebugLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claimCount atomic.Int32
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimCount.Add(1) == 1 {
				panic("store implementation panic")
			}
			cancel()
			return nil, nil
		},
	}
	options := validOptions()
	options.PollInterval = time.Millisecond
	relay, err := New(logger.Wrap(zap.New(core)), store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		t.Fatal("publisher called without a claimed message")
		return PublishAck{}, nil
	}), nil, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries := observed.FilterMessage("outbox store operation failed").All()
	if len(entries) != 1 {
		t.Fatalf("store failure log count = %d, want one", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["operation"] != "claim" || fields["failure_code"] != failureStore {
		t.Fatalf("store failure fields = %v, want claim/store_error", fields)
	}
}

func TestShutdownDeadLettersMalformedClaimInsteadOfPublishing(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.DebugLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var published atomic.Bool
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			cancel()
			return []ClaimedMessage{{
				Lease:   Lease{MessageID: "message-1", Token: "lease-token", Attempt: 1},
				Message: Message{ID: "message-1"},
			}}, nil
		},
		markDead: func(_ context.Context, lease Lease, disposition DeadDisposition) error {
			if lease.MessageID != "message-1" || disposition.Code != failureInvalidMessage {
				t.Fatalf("MarkDead = (%+v, %+v), want invalid-message lease", lease, disposition)
			}
			return nil
		},
	}
	relay, err := New(logger.Wrap(zap.New(core)), store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		published.Store(true)
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if published.Load() {
		t.Fatal("malformed claimed message reached publisher during shutdown")
	}
	entries := observed.FilterMessage("outbox message marked dead").All()
	if len(entries) != 1 || entries[0].ContextMap()["failure_code"] != failureInvalidMessage {
		t.Fatalf("invalid-message log entries = %+v", entries)
	}
}

func TestShutdownLogsWhenAClaimedMessageCannotBeReleased(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.DebugLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var published atomic.Bool
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			cancel()
			message := validMessage("message-1")
			return []ClaimedMessage{{
				Lease: Lease{MessageID: message.ID, Token: "lease-token", Attempt: 1}, Message: message,
			}}, nil
		},
		markRetry: func(_ context.Context, _ Lease, disposition RetryDisposition) error {
			if disposition.Code != failureShutdown || disposition.Delay != 0 {
				t.Fatalf("shutdown retry disposition = %+v", disposition)
			}
			return ErrLeaseLost
		},
	}
	relay, err := New(logger.Wrap(zap.New(core)), store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		published.Store(true)
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if published.Load() {
		t.Fatal("message was published after shutdown won the claim race")
	}
	entries := observed.FilterMessage("outbox store operation failed").All()
	if len(entries) != 1 {
		t.Fatalf("store failure log count = %d, want one", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["operation"] != "mark_retry" || fields["failure_code"] != "lease_lost" {
		t.Fatalf("store failure fields = %v, want mark_retry/lease_lost", fields)
	}
}
