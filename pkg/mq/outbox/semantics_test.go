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

func TestInvalidCorrelationIDIsMarkedDeadWithoutPublishing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	message := validMessage("message-1")
	message.Metadata.CorrelationID = "contains a space"
	var published atomic.Bool
	var claimed atomic.Bool
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease: Lease{MessageID: message.ID, Token: "token", Attempt: 1}, Message: message,
			}}, nil
		},
		markDead: func(markCtx context.Context, _ Lease, disposition DeadDisposition) error {
			if markCtx.Err() != nil {
				t.Fatalf("MarkDead context = %v, want live cleanup context", markCtx.Err())
			}
			if disposition != (DeadDisposition{Code: failureInvalidMessage}) {
				t.Fatalf("MarkDead disposition = %+v", disposition)
			}
			cancel()
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
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
		t.Fatal("invalid message reached publisher")
	}
}

func TestUnsafeMessageMetadataIsMarkedDeadWithoutPublishing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Message)
	}{
		{name: "destination control character", mutate: func(message *Message) { message.Destination = "events\ninjected" }},
		{name: "attribute key control character", mutate: func(message *Message) {
			message.Metadata.Attributes = []Attribute{{Key: "bad\nkey", Value: "value"}}
		}},
		{name: "attribute value control character", mutate: func(message *Message) {
			message.Metadata.Attributes = []Attribute{{Key: "key", Value: "bad\rvalue"}}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			message := validMessage("message-1")
			test.mutate(&message)
			var claimed atomic.Bool
			var published atomic.Bool
			store := storeFuncs{
				claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
					if claimed.Swap(true) {
						return nil, nil
					}
					return []ClaimedMessage{{
						Lease: Lease{MessageID: message.ID, Token: "token", Attempt: 1}, Message: message,
					}}, nil
				},
				markDead: func(context.Context, Lease, DeadDisposition) error {
					cancel()
					return nil
				},
			}
			relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
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
				t.Fatal("unsafe message reached publisher")
			}
		})
	}
}

func TestMessageDataExceedingConfiguredBudgetIsMarkedDeadWithoutPublishing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	message := validMessage("message-1")
	message.Key = []byte("key")
	message.Payload = []byte("payload")
	var claimed atomic.Bool
	var published atomic.Bool
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease: Lease{MessageID: message.ID, Token: "token", Attempt: 1}, Message: message,
			}}, nil
		},
		markDead: func(_ context.Context, _ Lease, disposition DeadDisposition) error {
			if disposition != (DeadDisposition{Code: failureInvalidMessage}) {
				t.Fatalf("MarkDead disposition = %+v", disposition)
			}
			cancel()
			return nil
		},
	}
	options := validOptions()
	options.MaxMessageBytes = len(message.Key) + len(message.Payload) - 1
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		published.Store(true)
		return PublishAck{}, nil
	}), nil, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if published.Load() {
		t.Fatal("oversized message reached publisher")
	}
}

func TestUnsafePublishReferenceIsDiscardedAfterSuccessfulPublish(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claimed atomic.Bool
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease:   Lease{MessageID: "message-1", Token: "token", Attempt: 1},
				Message: validMessage("message-1"),
			}}, nil
		},
		markPublished: func(_ context.Context, _ Lease, ack PublishAck) error {
			if ack.Reference != "" {
				t.Fatalf("MarkPublished reference = %q, want sanitized empty value", ack.Reference)
			}
			cancel()
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{Reference: "unsafe\nreference"}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestPublishedButUnfinalizedMessageIsNotExplicitlyRetried(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claimed atomic.Bool
	var retries atomic.Int32
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease: Lease{MessageID: "message-1", Token: "token", Attempt: 1}, Message: validMessage("message-1"),
			}}, nil
		},
		markPublished: func(context.Context, Lease, PublishAck) error {
			cancel()
			return errors.New("store unavailable")
		},
		markRetry: func(context.Context, Lease, RetryDisposition) error {
			retries.Add(1)
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{Reference: "remote-reference"}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := retries.Load(); got != 0 {
		t.Fatalf("MarkRetry calls = %d, want 0 after successful publish", got)
	}
}

func TestShutdownCancelsPublishAndFinalizesRetryWithZeroDelay(t *testing.T) {
	t.Parallel()

	publishStarted := make(chan struct{})
	finalized := make(chan struct{})
	var claimed atomic.Bool
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease: Lease{MessageID: "message-1", Token: "token", Attempt: 1}, Message: validMessage("message-1"),
			}}, nil
		},
		markRetry: func(markCtx context.Context, _ Lease, disposition RetryDisposition) error {
			if markCtx.Err() != nil {
				t.Fatalf("MarkRetry context = %v, want independent cleanup context", markCtx.Err())
			}
			if disposition != (RetryDisposition{Code: failureShutdown, Ambiguous: true}) {
				t.Fatalf("MarkRetry disposition = %+v", disposition)
			}
			close(finalized)
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(ctx context.Context, _ Message) (PublishAck, error) {
		close(publishStarted)
		<-ctx.Done()
		return PublishAck{}, ctx.Err()
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- relay.Run(context.Background()) }()
	<-publishStarted
	if err := relay.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-finalized:
	default:
		t.Fatal("Shutdown returned before retry finalization")
	}
	if err := <-runResult; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := relay.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated Shutdown: %v", err)
	}
	if err := relay.Run(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second Run error = %v, want ErrInvalidState", err)
	}
}

func TestShutdownReleasesClaimedMessagesThatHaveNotStartedPublishing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claimed atomic.Bool
	var published atomic.Int32
	var retried atomic.Int32
	var ambiguous atomic.Int32
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			messages := make([]ClaimedMessage, 2)
			for index := range messages {
				id := "message-" + string(rune('a'+index))
				messages[index] = ClaimedMessage{
					Lease: Lease{MessageID: id, Token: "token-" + id, Attempt: 1}, Message: validMessage(id),
				}
			}
			cancel()
			return messages, nil
		},
		markRetry: func(_ context.Context, _ Lease, disposition RetryDisposition) error {
			if disposition.Code != failureShutdown || disposition.Delay != 0 {
				t.Fatalf("MarkRetry disposition = %+v", disposition)
			}
			if disposition.Ambiguous {
				ambiguous.Add(1)
			}
			retried.Add(1)
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(ctx context.Context, _ Message) (PublishAck, error) {
		published.Add(1)
		return PublishAck{}, ctx.Err()
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- relay.Run(ctx) }()
	if err := <-runResult; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := published.Load(); got != 0 {
		t.Fatalf("Publish calls = %d, want none after cancellation won the claim race", got)
	}
	if got := retried.Load(); got != 2 {
		t.Fatalf("MarkRetry calls = %d, want both leases released", got)
	}
	if got := ambiguous.Load(); got != 0 {
		t.Fatalf("ambiguous retries = %d, want none before Publisher was called", got)
	}
}

func TestRelayNeverClaimsBeyondAvailableConcurrency(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	publishStarted := make(chan struct{}, 2)
	release := make(chan struct{})
	var claimCalls atomic.Int32
	store := storeFuncs{
		claim: func(_ context.Context, request ClaimRequest) ([]ClaimedMessage, error) {
			call := claimCalls.Add(1)
			if call > 1 {
				t.Fatalf("Claim called while all %d slots were occupied", request.Limit)
			}
			if request.Limit != 2 {
				t.Fatalf("first Claim limit = %d, want 2", request.Limit)
			}
			return []ClaimedMessage{
				{Lease: Lease{MessageID: "message-1", Token: "token-1", Attempt: 1}, Message: validMessage("message-1")},
				{Lease: Lease{MessageID: "message-2", Token: "token-2", Attempt: 1}, Message: validMessage("message-2")},
			}, nil
		},
		markPublished: func(context.Context, Lease, PublishAck) error { return nil },
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		publishStarted <- struct{}{}
		<-release
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- relay.Run(ctx) }()
	<-publishStarted
	<-publishStarted
	time.Sleep(25 * time.Millisecond)
	if got := claimCalls.Load(); got != 1 {
		t.Fatalf("Claim calls with full capacity = %d, want 1", got)
	}
	cancel()
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestStoreOverClaimFailsFastWithoutPublishing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var claimed atomic.Bool
	var published atomic.Int32
	var finalized atomic.Int32
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claimed.Swap(true) {
				return nil, nil
			}
			messages := make([]ClaimedMessage, 5)
			for index := range messages {
				id := "message-" + string(rune('a'+index))
				messages[index] = ClaimedMessage{
					Lease: Lease{MessageID: id, Token: "token-" + id, Attempt: 1}, Message: validMessage(id),
				}
			}
			return messages, nil
		},
		markPublished: func(context.Context, Lease, PublishAck) error {
			finalized.Add(1)
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		published.Add(1)
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); !errors.Is(err, ErrStoreContract) {
		t.Fatalf("Run error = %v, want ErrStoreContract", err)
	}
	if got := published.Load(); got != 0 {
		t.Fatalf("publish calls = %d, want none", got)
	}
	if got := finalized.Load(); got != 0 {
		t.Fatalf("finalization calls = %d, want leases to expire", got)
	}
}

func TestStoreIdentityContractViolationsFailWithoutPublishingOrFinalizing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claimed []ClaimedMessage
	}{
		{
			name: "lease message mismatch",
			claimed: []ClaimedMessage{{
				Lease:   Lease{MessageID: "message-a", Token: "token-a", Attempt: 1},
				Message: validMessage("message-b"),
			}},
		},
		{
			name: "duplicate message identity",
			claimed: []ClaimedMessage{
				{Lease: Lease{MessageID: "message-a", Token: "token-a", Attempt: 1}, Message: validMessage("message-a")},
				{Lease: Lease{MessageID: "message-a", Token: "token-b", Attempt: 2}, Message: validMessage("message-a")},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var claimed atomic.Bool
			var published atomic.Int32
			var finalized atomic.Int32
			store := storeFuncs{
				claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
					if claimed.Swap(true) {
						return nil, nil
					}
					return test.claimed, nil
				},
				markPublished: func(context.Context, Lease, PublishAck) error {
					finalized.Add(1)
					cancel()
					return nil
				},
				markRetry: func(context.Context, Lease, RetryDisposition) error {
					finalized.Add(1)
					cancel()
					return nil
				},
				markDead: func(context.Context, Lease, DeadDisposition) error {
					finalized.Add(1)
					cancel()
					return nil
				},
			}
			relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
				published.Add(1)
				cancel()
				return PublishAck{}, nil
			}), nil, validOptions())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := relay.Run(ctx); !errors.Is(err, ErrStoreContract) {
				t.Fatalf("Run error = %v, want ErrStoreContract", err)
			}
			if published.Load() != 0 || finalized.Load() != 0 {
				t.Fatalf("published=%d finalized=%d, want zero", published.Load(), finalized.Load())
			}
		})
	}
}

func TestStoreCannotReturnClaimsWithAnError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var claims atomic.Int32
	var published atomic.Int32
	var finalized atomic.Int32
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			if claims.Add(1) == 1 {
				return []ClaimedMessage{{
					Lease:   Lease{MessageID: "message-1", Token: "token", Attempt: 1},
					Message: validMessage("message-1"),
				}}, errors.New("partial claim")
			}
			return nil, nil
		},
		markPublished: func(context.Context, Lease, PublishAck) error { finalized.Add(1); return nil },
		markRetry:     func(context.Context, Lease, RetryDisposition) error { finalized.Add(1); return nil },
		markDead:      func(context.Context, Lease, DeadDisposition) error { finalized.Add(1); return nil },
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		published.Add(1)
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); !errors.Is(err, ErrStoreContract) {
		t.Fatalf("Run error = %v, want ErrStoreContract", err)
	}
	if published.Load() != 0 || finalized.Load() != 0 {
		t.Fatalf("published=%d finalized=%d, want zero", published.Load(), finalized.Load())
	}
}

func TestInvalidBackoffFallsBackToPollInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backoff Backoff
	}{
		{name: "panic", backoff: BackoffFunc(func(uint32) time.Duration { panic("broken backoff") })},
		{name: "negative", backoff: BackoffFunc(func(uint32) time.Duration { return -time.Second })},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var claimed atomic.Bool
			dispositions := make(chan RetryDisposition, 1)
			store := storeFuncs{
				claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
					if claimed.Swap(true) {
						return nil, nil
					}
					return []ClaimedMessage{{
						Lease:   Lease{MessageID: "message-1", Token: "token", Attempt: 1},
						Message: validMessage("message-1"),
					}}, nil
				},
				markRetry: func(_ context.Context, _ Lease, disposition RetryDisposition) error {
					dispositions <- disposition
					cancel()
					return nil
				},
			}
			options := validOptions()
			options.PollInterval = 17 * time.Millisecond
			options.Backoff = test.backoff
			relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
				return PublishAck{}, &PublishError{Code: "unavailable", Retryable: true}
			}), nil, options)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := relay.Run(ctx); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if disposition := <-dispositions; disposition.Delay != 17*time.Millisecond {
				t.Fatalf("retry delay = %s, want PollInterval fallback", disposition.Delay)
			}
		})
	}
}

func TestFatalStoreContractDrainsPreviouslyAdmittedWorkersBeforeRunReturns(t *testing.T) {
	t.Parallel()

	publishStarted := make(chan struct{}, 2)
	releasePublish := make(chan struct{})
	contractFailure := make(chan struct{})
	var claimCalls atomic.Int32
	var finalized atomic.Int32
	store := storeFuncs{
		claim: func(_ context.Context, request ClaimRequest) ([]ClaimedMessage, error) {
			switch claimCalls.Add(1) {
			case 1:
				return []ClaimedMessage{
					{Lease: Lease{MessageID: "message-1", Token: "token-1", Attempt: 1}, Message: validMessage("message-1")},
					{Lease: Lease{MessageID: "message-2", Token: "token-2", Attempt: 1}, Message: validMessage("message-2")},
				}, nil
			case 2:
				if request.Limit != 2 {
					t.Fatalf("second Claim limit = %d, want 2", request.Limit)
				}
				close(contractFailure)
				return []ClaimedMessage{
					{Lease: Lease{MessageID: "message-3", Token: "token-3", Attempt: 1}, Message: validMessage("message-3")},
					{Lease: Lease{MessageID: "message-4", Token: "token-4", Attempt: 1}, Message: validMessage("message-4")},
					{Lease: Lease{MessageID: "message-5", Token: "token-5", Attempt: 1}, Message: validMessage("message-5")},
				}, nil
			default:
				return nil, nil
			}
		},
		markPublished: func(context.Context, Lease, PublishAck) error {
			finalized.Add(1)
			return nil
		},
	}
	options := validOptions()
	options.Concurrency = 4
	options.ClaimLimit = 2
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		publishStarted <- struct{}{}
		<-releasePublish
		return PublishAck{}, nil
	}), nil, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- relay.Run(context.Background()) }()
	<-publishStarted
	<-publishStarted
	<-contractFailure
	select {
	case err := <-runResult:
		t.Fatalf("Run returned %v before admitted workers drained", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(releasePublish)
	if err := <-runResult; !errors.Is(err, ErrStoreContract) {
		t.Fatalf("Run error = %v, want ErrStoreContract", err)
	}
	if got := finalized.Load(); got != 2 {
		t.Fatalf("finalized workers = %d, want 2", got)
	}
}

func TestMissingCorrelationIDIsStableAcrossRetry(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claims atomic.Int32
	var publishes atomic.Int32
	correlationIDs := make(chan string, 2)
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			attempt := claims.Add(1)
			if attempt > 2 {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease:   Lease{MessageID: "message-1", Token: "token-" + string(rune('0'+attempt)), Attempt: uint32(attempt)},
				Message: validMessage("message-1"),
			}}, nil
		},
		markRetry: func(context.Context, Lease, RetryDisposition) error { return nil },
		markPublished: func(context.Context, Lease, PublishAck) error {
			cancel()
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(publishCtx context.Context, _ Message) (PublishAck, error) {
		correlationIDs <- correlation.FromContext(publishCtx)
		if publishes.Add(1) == 1 {
			return PublishAck{}, &PublishError{Code: "unavailable", Retryable: true}
		}
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	first, second := <-correlationIDs, <-correlationIDs
	if first == "" || first != second {
		t.Fatalf("correlation IDs = (%q, %q), want identical non-empty values", first, second)
	}
}

func TestPublisherPanicIsConvertedToRetry(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	store := storeFuncs{
		claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
			claimed := false
			once.Do(func() { claimed = true })
			if !claimed {
				return nil, nil
			}
			return []ClaimedMessage{{
				Lease: Lease{MessageID: "message-1", Token: "token", Attempt: 1}, Message: validMessage("message-1"),
			}}, nil
		},
		markRetry: func(_ context.Context, _ Lease, disposition RetryDisposition) error {
			if disposition.Code != failurePublisherPanic || !disposition.Ambiguous {
				t.Fatalf("MarkRetry disposition = %+v", disposition)
			}
			cancel()
			return nil
		},
	}
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		panic("publisher bug")
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestShutdownBeforeRunPreventsRun(t *testing.T) {
	t.Parallel()

	relay, err := New(inertStore{}, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{}, nil
	}), nil, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := relay.Run(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Run error = %v, want ErrInvalidState", err)
	}
}
