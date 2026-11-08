//go:build integration

package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestRedisLeaseIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	engine := newTestEngine(t)
	config := settings.Coordination{
		TTLMilliseconds:              250,
		RenewIntervalMilliseconds:    80,
		OperationTimeoutMilliseconds: 40,
		CleanupTimeoutMilliseconds:   1_000,
		Backoff:                      settings.Backoff{InitialDelayMilliseconds: 5},
	}
	adapter := NewLeaseAdapter(engine)
	ownerA, err := coordination.New(adapter, logger.Wrap(zap.NewNop()), config)
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := coordination.New(adapter, logger.Wrap(zap.NewNop()), config)
	if err != nil {
		t.Fatal(err)
	}

	leaseA, err := ownerA.TryAcquire(ctx, "resource")
	if err != nil {
		t.Fatalf("first acquisition: %v", err)
	}
	if _, err := ownerB.TryAcquire(ctx, "resource"); !errors.Is(err, coordination.ErrHeld) {
		t.Fatalf("contending acquisition error = %v; want ErrHeld", err)
	}

	otherEngine := &RedisEngine{client: engine.client, namespace: "other-application"}
	otherOwner, err := coordination.New(NewLeaseAdapter(otherEngine), logger.Wrap(zap.NewNop()), config)
	if err != nil {
		t.Fatal(err)
	}
	otherLease, err := otherOwner.TryAcquire(ctx, "resource")
	if err != nil {
		t.Fatalf("same resource in another namespace: %v", err)
	}
	if err := otherLease.Release(ctx); err != nil {
		t.Fatalf("release other namespace: %v", err)
	}

	leaseB := acquireAfterExpiry(t, ctx, ownerB, "resource")
	if err := leaseA.Renew(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
		t.Fatalf("expired owner renewal error = %v; want ErrLeaseLost", err)
	}
	if err := leaseA.Release(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
		t.Fatalf("expired owner release error = %v; want ErrLeaseLost", err)
	}
	if _, err := ownerA.TryAcquire(ctx, "resource"); !errors.Is(err, coordination.ErrHeld) {
		t.Fatalf("old owner released the new lease: acquisition error = %v", err)
	}
	if err := leaseB.Release(ctx); err != nil {
		t.Fatalf("release current owner: %v", err)
	}
	leaseC, err := ownerA.TryAcquire(ctx, "resource")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := leaseC.Release(ctx); err != nil {
		t.Fatalf("release final owner: %v", err)
	}
}

func acquireAfterExpiry(t *testing.T, ctx context.Context, client *coordination.Client, resource string) coordination.Lease {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		lease, err := client.TryAcquire(ctx, resource)
		if err == nil {
			return lease
		}
		if !errors.Is(err, coordination.ErrHeld) {
			t.Fatalf("acquisition while waiting for expiry: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("lease did not expire before deadline: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
