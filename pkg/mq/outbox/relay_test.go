package outbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/correlation"
)

type storeFuncs struct {
	claim         func(context.Context, ClaimRequest) ([]ClaimedMessage, error)
	markPublished func(context.Context, Lease, PublishAck) error
	markRetry     func(context.Context, Lease, RetryDisposition) error
	markDead      func(context.Context, Lease, DeadDisposition) error
}

func (store storeFuncs) Claim(ctx context.Context, request ClaimRequest) ([]ClaimedMessage, error) {
	return store.claim(ctx, request)
}

func (store storeFuncs) MarkPublished(ctx context.Context, lease Lease, ack PublishAck) error {
	if store.markPublished == nil {
		return nil
	}
	return store.markPublished(ctx, lease, ack)
}

func (store storeFuncs) MarkRetry(ctx context.Context, lease Lease, disposition RetryDisposition) error {
	if store.markRetry == nil {
		return nil
	}
	return store.markRetry(ctx, lease, disposition)
}

func (store storeFuncs) MarkDead(ctx context.Context, lease Lease, disposition DeadDisposition) error {
	if store.markDead == nil {
		return nil
	}
	return store.markDead(ctx, lease, disposition)
}

func TestRelayPublishesAndFinalizesWithCorrelationAndImmutableMessage(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := Message{
		ID:          "message-1",
		Destination: "events.created",
		Key:         []byte("key"),
		Payload:     []byte("payload"),
		Metadata:    Metadata{Attributes: []Attribute{{Key: "region", Value: "west"}}},
	}
	lease := Lease{MessageID: original.ID, Token: "opaque", Attempt: 1}
	var claims atomic.Int32

	store := storeFuncs{
		claim: func(_ context.Context, request ClaimRequest) ([]ClaimedMessage, error) {
			claimNumber := claims.Add(1)
			if request.Owner != "instance-1" || request.Limit < 1 || request.Limit > 2 || request.LeaseDuration != 5*time.Second {
				t.Fatalf("Claim request = %+v", request)
			}
			if claimNumber == 1 {
				return []ClaimedMessage{{Lease: lease, Message: original}}, nil
			}
			return nil, nil
		},
		markPublished: func(markCtx context.Context, gotLease Lease, ack PublishAck) error {
			if markCtx.Err() != nil {
				t.Fatalf("MarkPublished received canceled cleanup context: %v", markCtx.Err())
			}
			if gotLease != lease || ack.Reference != "remote-1" {
				t.Fatalf("MarkPublished = (%+v, %+v)", gotLease, ack)
			}
			cancel()
			return nil
		},
	}
	publisher := PublisherFunc(func(publishCtx context.Context, message Message) (PublishAck, error) {
		if message.Metadata.CorrelationID == "" {
			t.Fatal("publisher received empty correlation ID")
		}
		if got := correlation.FromContext(publishCtx); got != message.Metadata.CorrelationID {
			t.Fatalf("context cid = %q, message cid = %q", got, message.Metadata.CorrelationID)
		}
		message.Key[0] = 'X'
		message.Payload[0] = 'X'
		message.Metadata.Attributes[0].Value = "mutated"
		return PublishAck{Reference: "remote-1"}, nil
	})

	relay, err := New(store, publisher, nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(original.Key) != "key" || string(original.Payload) != "payload" || original.Metadata.Attributes[0].Value != "west" {
		t.Fatalf("publisher mutated store-owned message: %+v", original)
	}
}

func TestRelayPropagatesAnImmutableDestinationAllowlist(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	configured := []string{"events.created", "events.updated"}
	options := validOptions()
	options.Destinations = configured
	store := storeFuncs{
		claim: func(_ context.Context, request ClaimRequest) ([]ClaimedMessage, error) {
			if len(request.Destinations) != 2 || request.Destinations[0] != "events.created" ||
				request.Destinations[1] != "events.updated" {
				t.Fatalf("Claim destinations = %v", request.Destinations)
			}
			request.Destinations[0] = "mutated-by-store"
			cancel()
			return nil, nil
		},
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{}, nil
	}), nil, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	configured[0] = "mutated-by-caller"
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if relay.options.Destinations[0] != "events.created" {
		t.Fatalf("relay allowlist was mutated: %v", relay.options.Destinations)
	}
}

func TestRelayRejectsStoreDestinationOutsideClaimAllowlist(t *testing.T) {
	t.Parallel()

	claimed := false
	published := atomic.Int32{}
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed {
				return nil, nil
			}
			claimed = true
			message := validMessage("message-1")
			message.Destination = "events.forbidden"
			return []ClaimedMessage{{
				Lease:   Lease{MessageID: message.ID, Token: "token", Attempt: 1},
				Message: message,
			}}, nil
		},
	}
	options := validOptions()
	options.Destinations = []string{"events.allowed"}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		published.Add(1)
		return PublishAck{}, nil
	}), nil, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(context.Background()); !errors.Is(err, ErrStoreContract) {
		t.Fatalf("Run error = %v, want ErrStoreContract", err)
	}
	if published.Load() != 0 {
		t.Fatal("publisher received a message outside the claim allowlist")
	}
}

func TestRelayClassifiesPublishFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		attempt     uint32
		maxAttempts uint32
		failure     error
		wantRetry   *RetryDisposition
		wantDead    *DeadDisposition
	}{
		{
			name:    "retryable",
			attempt: 2,
			failure: &PublishError{Code: "unavailable", Retryable: true, Ambiguous: false},
			wantRetry: &RetryDisposition{
				Delay: 2 * time.Millisecond, Code: "unavailable", Ambiguous: false,
			},
		},
		{
			name:     "permanent",
			attempt:  1,
			failure:  &PublishError{Code: "rejected", Retryable: false, Ambiguous: false},
			wantDead: &DeadDisposition{Code: "rejected", Ambiguous: false},
		},
		{
			name:    "typed permanent cancellation cause is not shutdown",
			attempt: 1,
			failure: &PublishError{
				Code: "permanent_cancel", Retryable: false, Cause: context.Canceled,
			},
			wantDead: &DeadDisposition{Code: "permanent_cancel", Ambiguous: false},
		},
		{
			name:    "untyped is retryable and ambiguous",
			attempt: 3,
			failure: errors.New("transport failed"),
			wantRetry: &RetryDisposition{
				Delay: 3 * time.Millisecond, Code: "publish_error", Ambiguous: true,
			},
		},
		{
			name:    "unsafe failure code is normalized",
			attempt: 1,
			failure: &PublishError{Code: "bad\ncode", Retryable: true},
			wantRetry: &RetryDisposition{
				Delay: time.Millisecond, Code: "publish_error", Ambiguous: false,
			},
		},
		{
			name:        "maximum attempts",
			attempt:     3,
			maxAttempts: 3,
			failure:     &PublishError{Code: "unavailable", Retryable: true, Ambiguous: true},
			wantDead:    &DeadDisposition{Code: "max_attempts_exceeded", Ambiguous: true},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lease := Lease{MessageID: "message-1", Token: "token", Attempt: test.attempt}
			var once sync.Once
			store := storeFuncs{
				claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
					claimed := false
					once.Do(func() { claimed = true })
					if !claimed {
						return nil, nil
					}
					return []ClaimedMessage{{Lease: lease, Message: validMessage("message-1")}}, nil
				},
				markRetry: func(_ context.Context, got Lease, disposition RetryDisposition) error {
					cancel()
					if test.wantRetry == nil || got != lease || disposition != *test.wantRetry {
						t.Fatalf("MarkRetry = (%+v, %+v), want (%+v, %+v)", got, disposition, lease, test.wantRetry)
					}
					return nil
				},
				markDead: func(_ context.Context, got Lease, disposition DeadDisposition) error {
					cancel()
					if test.wantDead == nil || got != lease || disposition != *test.wantDead {
						t.Fatalf("MarkDead = (%+v, %+v), want (%+v, %+v)", got, disposition, lease, test.wantDead)
					}
					return nil
				},
			}
			options := validOptions()
			options.MaxAttempts = test.maxAttempts
			relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
				return PublishAck{}, test.failure
			}), nil, options)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := relay.Run(ctx); err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
	}
}

func TestRelayZeroValueRejectsLifecycleCalls(t *testing.T) {
	t.Parallel()

	var relay Relay
	if err := relay.Run(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Run() error = %v, want ErrInvalidState", err)
	}
	if err := relay.Shutdown(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Shutdown() error = %v, want ErrInvalidState", err)
	}
}

func validMessage(id string) Message {
	return Message{ID: id, Destination: "events", Payload: []byte("{}")}
}
