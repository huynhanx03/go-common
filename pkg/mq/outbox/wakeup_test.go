package outbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWakeupAcceleratesPollingButPollingRemainsCorrectnessFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wakeup     Wakeup
		poll       time.Duration
		trigger    func()
		wantWithin time.Duration
	}{
		func() struct {
			name       string
			wakeup     Wakeup
			poll       time.Duration
			trigger    func()
			wantWithin time.Duration
		} {
			signal := make(chan struct{}, 1)
			return struct {
				name       string
				wakeup     Wakeup
				poll       time.Duration
				trigger    func()
				wantWithin time.Duration
			}{
				name: "wakeup", poll: time.Second, wantWithin: 200 * time.Millisecond,
				wakeup: WakeupFunc(func(ctx context.Context) error {
					select {
					case <-signal:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}),
				trigger: func() { signal <- struct{}{} },
			}
		}(),
		{
			name: "poll fallback", poll: 15 * time.Millisecond, wantWithin: 300 * time.Millisecond,
			wakeup: WakeupFunc(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}),
			trigger: func() {},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			firstClaim := make(chan struct{})
			published := make(chan struct{})
			var claims atomic.Int32
			store := storeFuncs{
				claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
					claimNumber := claims.Add(1)
					if claimNumber == 1 {
						close(firstClaim)
						return nil, nil
					}
					if claimNumber != 2 {
						return nil, nil
					}
					return []ClaimedMessage{{
						Lease:   Lease{MessageID: "message-1", Token: "token", Attempt: 1},
						Message: validMessage("message-1"),
					}}, nil
				},
				markPublished: func(context.Context, Lease, PublishAck) error {
					cancel()
					return nil
				},
			}
			options := validOptions()
			options.PollInterval = test.poll
			relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
				close(published)
				return PublishAck{}, nil
			}), test.wakeup, options)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			result := make(chan error, 1)
			go func() { result <- relay.Run(ctx) }()
			<-firstClaim
			started := time.Now()
			test.trigger()
			select {
			case <-published:
				if elapsed := time.Since(started); elapsed > test.wantWithin {
					t.Fatalf("message published after %s, want within %s", elapsed, test.wantWithin)
				}
			case <-time.After(test.wantWithin):
				t.Fatalf("message was not published within %s", test.wantWithin)
			}
			if err := <-result; err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
	}
}

func TestWakeupFailuresAreRateLimited(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	var waits atomic.Int32
	wakeup := WakeupFunc(func(context.Context) error {
		waits.Add(1)
		return context.DeadlineExceeded
	})
	store := storeFuncs{claim: func(context.Context, ClaimRequest) ([]ClaimedMessage, error) {
		return nil, nil
	}}
	options := validOptions()
	options.PollInterval = 10 * time.Millisecond
	relay, err := New(store, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{}, nil
	}), wakeup, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := waits.Load(); got == 0 || got > 12 {
		t.Fatalf("Wakeup.Wait calls = %d, want bounded non-zero calls", got)
	}
}

func TestShutdownDoesNotDependOnMisbehavingWakeup(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	wakeup := WakeupFunc(func(context.Context) error {
		close(entered)
		<-release
		return nil
	})
	relay, err := New(inertStore{}, PublisherFunc(func(context.Context, Message) (PublishAck, error) {
		return PublishAck{}, nil
	}), wakeup, validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- relay.Run(context.Background()) }()
	<-entered
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := relay.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-runResult; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
