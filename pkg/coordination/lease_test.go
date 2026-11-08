package coordination

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestLeaseExecution(t *testing.T) {
	t.Run("acquire failure never starts the callback", func(t *testing.T) {
		want := errors.New("adapter unavailable")
		adapter := &testAdapter{acquire: func(context.Context, AcquireRequest) (Lease, error) {
			return nil, want
		}}
		client := newTestLeaseClient(t, adapter)
		var callbacks atomic.Int32
		err := client.Do(context.Background(), "resource", func(context.Context) error {
			callbacks.Add(1)
			return nil
		})
		if !errors.Is(err, want) || callbacks.Load() != 0 {
			t.Fatalf("Do() = %v, callback count %d; want adapter error and no callback", err, callbacks.Load())
		}
	})

	t.Run("callback error and release error are both preserved", func(t *testing.T) {
		callbackErr := errors.New("work failed")
		releaseErr := errors.New("release failed")
		lease := &testLease{release: func(context.Context) error { return releaseErr }}
		client := newTestLeaseClient(t, readyAdapter(lease))
		err := client.Do(context.Background(), "resource", func(context.Context) error { return callbackErr })
		if !errors.Is(err, callbackErr) || !errors.Is(err, releaseErr) {
			t.Fatalf("Do() error = %v; want callback and release causes", err)
		}
	})

	t.Run("uncertain acquisition with a lease is cleaned up", func(t *testing.T) {
		acquireErr := errors.New("acquire response is ambiguous")
		lease := &testLease{}
		adapter := &testAdapter{acquire: func(context.Context, AcquireRequest) (Lease, error) {
			return lease, acquireErr
		}}
		client := newTestLeaseClient(t, adapter)
		var callbacks atomic.Int32
		err := client.Do(context.Background(), "resource", func(context.Context) error {
			callbacks.Add(1)
			return nil
		})
		if !errors.Is(err, acquireErr) || callbacks.Load() != 0 || lease.releases.Load() != 1 {
			t.Fatalf("Do() = %v, callbacks %d, releases %d; want error, no work and cleanup", err, callbacks.Load(), lease.releases.Load())
		}
	})

	t.Run("renewal loss cancels work and releases ownership", func(t *testing.T) {
		renewCause := errors.New("renew transport failed")
		renewed := make(chan struct{})
		var renewOnce sync.Once
		lease := &testLease{
			renew: func(context.Context) error {
				renewOnce.Do(func() { close(renewed) })
				return renewCause
			},
		}
		client := newTestLeaseClient(t, readyAdapter(lease))
		err := client.Do(context.Background(), "resource", func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, ErrLeaseLost) || !errors.Is(err, renewCause) {
			t.Fatalf("Do() error = %v; want lease loss and adapter cause", err)
		}
		select {
		case <-renewed:
		default:
			t.Fatal("renewal was not attempted")
		}
		if lease.releases.Load() != 1 {
			t.Fatalf("release count = %d; want 1", lease.releases.Load())
		}
	})

	t.Run("callback completion joins an in-flight renewal before release", func(t *testing.T) {
		renewStarted := make(chan struct{})
		allowRenew := make(chan struct{})
		renewFinished := make(chan struct{})
		callbackReturned := make(chan struct{})
		lease := &testLease{
			renew: func(context.Context) error {
				close(renewStarted)
				<-allowRenew
				close(renewFinished)
				return nil
			},
			release: func(context.Context) error {
				select {
				case <-renewFinished:
					return nil
				default:
					return errors.New("released while renewal was in flight")
				}
			},
		}
		client := newTestLeaseClient(t, readyAdapter(lease))
		done := make(chan error, 1)
		go func() {
			done <- client.Do(context.Background(), "resource", func(context.Context) error {
				<-renewStarted
				close(callbackReturned)
				return nil
			})
		}()
		<-callbackReturned
		select {
		case <-renewFinished:
			t.Fatal("renewal completed before the test released it")
		default:
		}
		close(allowRenew)
		if err := <-done; err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if lease.renews.Load() != 1 || lease.releases.Load() != 1 {
			t.Fatalf("renewals %d, releases %d; want one joined renewal and one release", lease.renews.Load(), lease.releases.Load())
		}
	})

	t.Run("release uses bounded live context after caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		lease := &testLease{release: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, ok := ctx.Deadline(); !ok {
				return errors.New("cleanup context has no deadline")
			}
			return nil
		}}
		client := newTestLeaseClient(t, readyAdapter(lease))
		err := client.Do(ctx, "resource", func(context.Context) error {
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do() error = %v; want canceled caller context", err)
		}
		if lease.releases.Load() != 1 {
			t.Fatalf("release count = %d; want 1", lease.releases.Load())
		}
	})

	t.Run("panic releases the lease and preserves the panic", func(t *testing.T) {
		lease := &testLease{}
		client := newTestLeaseClient(t, readyAdapter(lease))
		defer func() {
			if got := recover(); got != "callback panic" {
				t.Fatalf("panic = %v; want original value", got)
			}
			if lease.releases.Load() != 1 {
				t.Fatalf("release count = %d; want 1", lease.releases.Load())
			}
		}()
		_ = client.Do(context.Background(), "resource", func(context.Context) error { panic("callback panic") })
	})

	t.Run("late successful acquisition is released and not returned", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		lease := &testLease{}
		adapter := &testAdapter{acquire: func(ctx context.Context, _ AcquireRequest) (Lease, error) {
			<-ctx.Done()
			return lease, nil
		}}
		client := newTestLeaseClient(t, adapter)
		if _, err := client.TryAcquire(ctx, "resource"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("TryAcquire() error = %v; want deadline", err)
		}
		if lease.releases.Load() != 1 {
			t.Fatalf("release count = %d; want late lease cleanup", lease.releases.Load())
		}
	})

	t.Run("backoff wait observes cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		adapter := &testAdapter{acquire: func(context.Context, AcquireRequest) (Lease, error) {
			return nil, ErrHeld
		}}
		config := testCoordinationConfig()
		config.AcquireTimeoutMilliseconds = 1000
		config.Backoff = settings.Backoff{InitialDelayMilliseconds: 3_600_000, MaxDelayMilliseconds: 3_600_000}
		client, err := New(adapter, logger.Wrap(zap.NewNop()), config)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- client.Do(ctx, "resource", func(context.Context) error { return nil }) }()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Do() error = %v; want cancellation", err)
		}
	})

	t.Run("contention retries use one owner until acquired", func(t *testing.T) {
		var calls atomic.Int32
		var firstOwner string
		lease := &testLease{}
		adapter := &testAdapter{acquire: func(_ context.Context, request AcquireRequest) (Lease, error) {
			if calls.Add(1) == 1 {
				firstOwner = request.Owner
				return nil, ErrHeld
			}
			if request.Owner != firstOwner {
				t.Errorf("owner changed between attempts: %q then %q", firstOwner, request.Owner)
			}
			return lease, nil
		}}
		config := testCoordinationConfig()
		config.AcquireTimeoutMilliseconds = 1000
		config.Backoff = settings.Backoff{InitialDelayMilliseconds: 1, MaxDelayMilliseconds: 1}
		client, err := New(adapter, logger.Wrap(zap.NewNop()), config)
		if err != nil {
			t.Fatal(err)
		}
		var callbacks atomic.Int32
		if err := client.Do(context.Background(), "resource", func(context.Context) error {
			callbacks.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if calls.Load() != 2 || callbacks.Load() != 1 || lease.releases.Load() != 1 {
			t.Fatalf("acquires %d, callbacks %d, releases %d; want (2, 1, 1)", calls.Load(), callbacks.Load(), lease.releases.Load())
		}
	})
}

func TestFencedLeaseExecution(t *testing.T) {
	t.Run("invalid grant is released before callback", func(t *testing.T) {
		lease := &testFencedLease{grant: Grant{Resource: "other", Owner: "owner", Fence: FenceToken{Authority: "db", Value: 1}}}
		adapter := &testFencedAdapter{acquire: func(context.Context, AcquireRequest) (FencedLease, error) { return lease, nil }}
		client, err := NewFenced(adapter, logger.Wrap(zap.NewNop()), testCoordinationConfig())
		if err != nil {
			t.Fatal(err)
		}
		var callbacks atomic.Int32
		err = client.Do(context.Background(), "resource", func(context.Context, Grant) error {
			callbacks.Add(1)
			return nil
		})
		if !errors.Is(err, ErrInvalidFence) || callbacks.Load() != 0 || lease.releases.Load() != 1 {
			t.Fatalf("Do() = %v, callbacks %d, releases %d; want invalid grant cleaned before work", err, callbacks.Load(), lease.releases.Load())
		}
	})

	t.Run("callback receives the verified grant", func(t *testing.T) {
		grant := Grant{Resource: "resource", Owner: "owner", Fence: FenceToken{Authority: "etcd/cluster", Value: 7}}
		lease := &testFencedLease{grant: grant}
		adapter := &testFencedAdapter{acquire: func(_ context.Context, request AcquireRequest) (FencedLease, error) {
			grant.Owner = request.Owner
			lease.grant = grant
			return lease, nil
		}}
		client, err := NewFenced(adapter, logger.Wrap(zap.NewNop()), testCoordinationConfig())
		if err != nil {
			t.Fatal(err)
		}
		var got Grant
		if err := client.Do(context.Background(), grant.Resource, func(_ context.Context, actual Grant) error {
			got = actual
			return nil
		}); err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if got.Resource != grant.Resource || got.Owner == "" || got.Fence != grant.Fence {
			t.Fatalf("callback grant = %+v; want verified resource, owner and fence", got)
		}
	})

	t.Run("manual acquisitions use distinct owners", func(t *testing.T) {
		owners := make(chan string, 2)
		adapter := &testFencedAdapter{acquire: func(_ context.Context, request AcquireRequest) (FencedLease, error) {
			owners <- request.Owner
			return &testFencedLease{grant: Grant{Resource: request.Resource, Owner: request.Owner, Fence: FenceToken{Authority: "etcd/cluster", Value: 1}}}, nil
		}}
		client, err := NewFenced(adapter, logger.Wrap(zap.NewNop()), testCoordinationConfig())
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			lease, err := client.TryAcquire(context.Background(), "resource")
			if err != nil {
				t.Fatalf("TryAcquire() error = %v", err)
			}
			if err := lease.Release(context.Background()); err != nil {
				t.Fatalf("Release() error = %v", err)
			}
		}
		first, second := <-owners, <-owners
		if first == "" || second == "" || first == second {
			t.Fatalf("owners = %q, %q; want unique nonempty values", first, second)
		}
	})
}

type testAdapter struct {
	acquire func(context.Context, AcquireRequest) (Lease, error)
}

func (adapter *testAdapter) TryAcquire(ctx context.Context, request AcquireRequest) (Lease, error) {
	return adapter.acquire(ctx, request)
}

type testFencedAdapter struct {
	acquire func(context.Context, AcquireRequest) (FencedLease, error)
}

func (adapter *testFencedAdapter) TryAcquireFenced(ctx context.Context, request AcquireRequest) (FencedLease, error) {
	return adapter.acquire(ctx, request)
}

type testLease struct {
	renew    func(context.Context) error
	release  func(context.Context) error
	renews   atomic.Int32
	releases atomic.Int32
}

func (lease *testLease) Renew(ctx context.Context) error {
	lease.renews.Add(1)
	if lease.renew != nil {
		return lease.renew(ctx)
	}
	return nil
}

func (lease *testLease) Release(ctx context.Context) error {
	lease.releases.Add(1)
	if lease.release != nil {
		return lease.release(ctx)
	}
	return nil
}

type testFencedLease struct {
	testLease
	grant Grant
}

func (lease *testFencedLease) Grant() Grant { return lease.grant }

func readyAdapter(lease Lease) *testAdapter {
	return &testAdapter{acquire: func(context.Context, AcquireRequest) (Lease, error) { return lease, nil }}
}

func newTestLeaseClient(t *testing.T, adapter Adapter) *Client {
	t.Helper()
	client, err := New(adapter, logger.Wrap(zap.NewNop()), testCoordinationConfig())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testCoordinationConfig() settings.Coordination {
	return settings.Coordination{
		TTLMilliseconds:              120,
		RenewIntervalMilliseconds:    30,
		OperationTimeoutMilliseconds: 10,
		CleanupTimeoutMilliseconds:   1000,
		Backoff:                      settings.Backoff{InitialDelayMilliseconds: 1, MaxDelayMilliseconds: 1},
	}
}
