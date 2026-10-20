package outbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type inertStore struct{}

func (inertStore) Claim(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
	return nil, nil
}

func (inertStore) MarkPublished(context.Context, Lease, PublishAck) error { return nil }
func (inertStore) MarkRetry(context.Context, Lease, RetryDisposition) error {
	return nil
}
func (inertStore) MarkDead(context.Context, Lease, DeadDisposition) error { return nil }

func validOptions() Options {
	return Options{
		Name:            "outbox-relay",
		Owner:           "instance-1",
		Concurrency:     2,
		ClaimLimit:      2,
		MaxMessageBytes: 1 << 20,
		MaxAttempts:     5,
		LeaseDuration:   5 * time.Second,
		PublishTimeout:  time.Second,
		StoreTimeout:    time.Second,
		FinalizeTimeout: time.Second,
		PollInterval:    10 * time.Millisecond,
		Backoff: BackoffFunc(func(attempt uint32) time.Duration {
			return time.Duration(attempt) * time.Millisecond
		}),
	}
}

func TestNewValidatesDependenciesAndOptions(t *testing.T) {
	t.Parallel()

	publisher := PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{}, nil
	})

	tests := []struct {
		name      string
		store     Store
		publisher Publisher
		mutate    func(*Options)
	}{
		{name: "nil store", publisher: publisher},
		{name: "nil publisher", store: inertStore{}},
		{name: "empty name", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Name = "" }},
		{name: "unsafe name", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Name = "relay\ninjected" }},
		{name: "empty owner", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Owner = "" }},
		{name: "unsafe owner", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Owner = "owner\rinjected" }},
		{name: "zero concurrency", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Concurrency = 0 }},
		{name: "claim over concurrency", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.ClaimLimit = 3 }},
		{name: "unsafe destination", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Destinations = []string{"events\ninjected"} }},
		{name: "duplicate destination", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Destinations = []string{"events", "events"} }},
		{name: "too many destinations", store: inertStore{}, publisher: publisher, mutate: func(o *Options) {
			o.Destinations = make([]string, maximumClaimDestinations+1)
			for index := range o.Destinations {
				o.Destinations[index] = fmt.Sprintf("events.%d", index)
			}
		}},
		{name: "zero message budget", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.MaxMessageBytes = 0 }},
		{name: "zero lease", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.LeaseDuration = 0 }},
		{name: "short lease", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.LeaseDuration = 3 * time.Second }},
		{name: "operation budget overflow", store: inertStore{}, publisher: publisher, mutate: func(o *Options) {
			o.LeaseDuration = time.Duration(1<<63 - 1)
			o.StoreTimeout = time.Duration(1<<63 - 1)
		}},
		{name: "zero publish timeout", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.PublishTimeout = 0 }},
		{name: "zero store timeout", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.StoreTimeout = 0 }},
		{name: "zero finalize timeout", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.FinalizeTimeout = 0 }},
		{name: "zero poll interval", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.PollInterval = 0 }},
		{name: "nil backoff", store: inertStore{}, publisher: publisher, mutate: func(o *Options) { o.Backoff = nil }},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := validOptions()
			if test.mutate != nil {
				test.mutate(&options)
			}
			_, err := New(test.store, test.publisher, nil, options)
			if !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("New error = %v, want ErrInvalidOptions", err)
			}
		})
	}
}

func TestNewAcceptsValidOptionsAndHelpersDelegate(t *testing.T) {
	t.Parallel()

	wakeupCalls := 0
	wakeup := WakeupFunc(func(context.Context) error {
		wakeupCalls++
		return nil
	})
	publishCalls := 0
	publisher := PublisherFunc(func(_ context.Context, message Message) (PublishAck, error) {
		publishCalls++
		return PublishAck{Reference: message.ID}, nil
	})

	relay, err := New(inertStore{}, publisher, wakeup, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if relay.Name() != "outbox-relay" {
		t.Fatalf("Name = %q, want outbox-relay", relay.Name())
	}
	ack, err := publisher.Publish(context.Background(), Message{ID: "m-1"})
	if err != nil || ack.Reference != "m-1" || publishCalls != 1 {
		t.Fatalf("PublisherFunc delegation = (%+v, %v, calls=%d)", ack, err, publishCalls)
	}
	if err := wakeup.Wait(context.Background()); err != nil || wakeupCalls != 1 {
		t.Fatalf("WakeupFunc delegation = (%v, calls=%d)", err, wakeupCalls)
	}

	want := 7 * time.Millisecond
	backoff := BackoffFunc(func(uint32) time.Duration { return want })
	if got := backoff.Delay(3); got != want {
		t.Fatalf("BackoffFunc.Delay = %s, want %s", got, want)
	}
}
