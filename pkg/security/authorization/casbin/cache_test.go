package casbin_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	commoncache "github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
	authcasbin "github.com/huynhanx03/go-common/pkg/security/authorization/casbin"
)

func TestRuntimeCacheFetchesAndReusesScopedRevisions(t *testing.T) {
	t.Parallel()

	cache := newRuntimeTestCache(t, newMemoryL1())
	runtimeCache, err := authcasbin.NewRuntimeCache(cache, time.Minute, authcasbin.WithClock(fixedNow))
	if err != nil {
		t.Fatal(err)
	}

	scope := authorization.Scope{Type: "tenant", ID: "acme"}
	loads := 0
	load := func(_ context.Context) (authcasbin.Snapshot, error) {
		loads++
		return snapshot(1, "read"), nil
	}

	first, err := runtimeCache.Fetch(context.Background(), scope, 1, load)
	if err != nil {
		t.Fatalf("Fetch(first) error = %v", err)
	}
	second, err := runtimeCache.Fetch(context.Background(), scope, 1, load)
	if err != nil {
		t.Fatalf("Fetch(cached) error = %v", err)
	}
	if first != second || loads != 1 {
		t.Fatalf("Fetch reused runtime = %t, loader calls = %d; want true, 1", first == second, loads)
	}

	otherScope := authorization.Scope{Type: "tenant", ID: "other"}
	if _, err := runtimeCache.Fetch(context.Background(), otherScope, 1, load); err != nil {
		t.Fatalf("Fetch(other scope) error = %v", err)
	}
	if _, err := runtimeCache.Fetch(context.Background(), scope, 2, func(context.Context) (authcasbin.Snapshot, error) {
		return snapshot(2, "write"), nil
	}); err != nil {
		t.Fatalf("Fetch(new revision) error = %v", err)
	}
	if loads != 2 {
		t.Fatalf("loader calls after distinct scope and revision = %d, want 2", loads)
	}
}

func TestRuntimeCacheRejectsARevisionThatBecameUnhealthy(t *testing.T) {
	t.Parallel()

	runtimeCache, err := authcasbin.NewRuntimeCache(
		newRuntimeTestCache(t, newMemoryL1()),
		time.Minute,
		authcasbin.WithClock(fixedNow),
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := authorization.Scope{Type: "tenant", ID: "acme"}
	loader := func(context.Context) (authcasbin.Snapshot, error) {
		return snapshot(1, "read"), nil
	}
	runtime, err := runtimeCache.Fetch(context.Background(), scope, 1, loader)
	if err != nil {
		t.Fatal(err)
	}
	runtime.SetDesiredRevision(2)

	if _, err := runtimeCache.Fetch(context.Background(), scope, 1, loader); !errors.Is(err, authcasbin.ErrUnhealthy) {
		t.Fatalf("Fetch(unhealthy cached runtime) error = %v", err)
	}
}

func TestRuntimeCacheRejectsInvalidAndUnusableSnapshots(t *testing.T) {
	t.Parallel()

	cache := newRuntimeTestCache(t, newMemoryL1())
	runtimeCache, err := authcasbin.NewRuntimeCache(cache, time.Minute, authcasbin.WithClock(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope := authorization.Scope{Type: "tenant", ID: "acme"}

	if _, err := runtimeCache.Fetch(ctx, authorization.Scope{}, 1, func(context.Context) (authcasbin.Snapshot, error) {
		return snapshot(1, "read"), nil
	}); !errors.Is(err, authorization.ErrInvalidRequest) {
		t.Fatalf("Fetch(invalid scope) error = %v", err)
	}
	if _, err := runtimeCache.Fetch(ctx, scope, 0, func(context.Context) (authcasbin.Snapshot, error) {
		return snapshot(1, "read"), nil
	}); !errors.Is(err, authcasbin.ErrInvalidSnapshot) {
		t.Fatalf("Fetch(invalid revision) error = %v", err)
	}
	if _, err := runtimeCache.Fetch(ctx, scope, 1, nil); !errors.Is(err, authcasbin.ErrInvalidSnapshot) {
		t.Fatalf("Fetch(nil loader) error = %v", err)
	}

	loaderErr := errors.New("snapshot unavailable")
	if _, err := runtimeCache.Fetch(ctx, scope, 1, func(context.Context) (authcasbin.Snapshot, error) {
		return authcasbin.Snapshot{}, loaderErr
	}); !errors.Is(err, loaderErr) {
		t.Fatalf("Fetch(loader error) error = %v", err)
	}
	if _, err := runtimeCache.Fetch(ctx, scope, 1, func(context.Context) (authcasbin.Snapshot, error) {
		return snapshot(2, "read"), nil
	}); !errors.Is(err, authcasbin.ErrStaleRevision) {
		t.Fatalf("Fetch(stale snapshot) error = %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := runtimeCache.Fetch(cancelled, scope, 1, func(context.Context) (authcasbin.Snapshot, error) {
		return snapshot(1, "read"), nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch(cancelled) error = %v", err)
	}
}

func TestRuntimeCacheRequiresLocalTierAndValidConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := authcasbin.NewRuntimeCache(nil, time.Minute); !errors.Is(err, authcasbin.ErrInvalidCache) {
		t.Fatalf("NewRuntimeCache(nil) error = %v", err)
	}
	if _, err := authcasbin.NewRuntimeCache(newRuntimeTestCache(t, newMemoryL1()), 0); !errors.Is(err, authcasbin.ErrInvalidCache) {
		t.Fatalf("NewRuntimeCache(zero TTL) error = %v", err)
	}
	if _, err := authcasbin.NewRuntimeCache(newRuntimeTestCache(t, newMemoryL1()), time.Minute, nil); !errors.Is(err, authcasbin.ErrInvalidOption) {
		t.Fatalf("NewRuntimeCache(nil option) error = %v", err)
	}

	remoteOnly, err := commoncache.New(commoncache.Config{
		L2:     &emptyL2{},
		Logger: logger.Wrap(zap.NewNop()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer remoteOnly.Close()
	if _, err := authcasbin.NewRuntimeCache(remoteOnly, time.Minute); !errors.Is(err, authcasbin.ErrInvalidCache) {
		t.Fatalf("NewRuntimeCache(L2-only) error = %v", err)
	}
}

func newRuntimeTestCache(t *testing.T, engine *memoryL1) *commoncache.Cache {
	t.Helper()

	cache, err := commoncache.New(commoncache.Config{
		L1:     engine,
		Logger: logger.Wrap(zap.NewNop()),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	return cache
}

type memoryL1 struct {
	mu       sync.Mutex
	values   map[string]any
	versions map[string]string
}

func newMemoryL1() *memoryL1 {
	return &memoryL1{values: make(map[string]any), versions: make(map[string]string)}
}

func (cache *memoryL1) Get(_ context.Context, key string) (any, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	value, ok := cache.values[key]
	return value, ok
}

func (cache *memoryL1) SetWithTTL(_ context.Context, key string, value any, _ time.Duration) bool {
	cache.mu.Lock()
	cache.values[key] = value
	cache.mu.Unlock()
	return true
}

func (cache *memoryL1) SnapshotVersion(_ context.Context, key string) (string, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.versions[key] == "" {
		cache.versions[key] = "initial"
	}
	return cache.versions[key], nil
}

func (cache *memoryL1) SetWithVersion(_ context.Context, versionKey, storageKey string, value any, _ time.Duration, token string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.versions[versionKey] == "" {
		cache.versions[versionKey] = "initial"
	}
	if cache.versions[versionKey] == token {
		cache.values[storageKey] = value
	}
	return nil
}

func (cache *memoryL1) DeleteValueWithVersion(_ context.Context, versionKey, storageKey, token string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.versions[versionKey] == token {
		delete(cache.values, storageKey)
	}
	return nil
}

func (cache *memoryL1) Revoke(_ context.Context, key string) error {
	cache.mu.Lock()
	cache.versions[key] = "revoked"
	delete(cache.values, key)
	cache.mu.Unlock()
	return nil
}

func (cache *memoryL1) RevokeMany(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if err := cache.Revoke(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (cache *memoryL1) Close() {}

type emptyL2 struct{}

func (emptyL2) Get(context.Context, string) ([]byte, bool, error)       { return nil, false, nil }
func (emptyL2) SnapshotVersion(context.Context, string) (string, error) { return "initial", nil }
func (emptyL2) SetWithVersion(context.Context, string, string, []byte, time.Duration, string) error {
	return nil
}
func (emptyL2) DeleteValueWithVersion(context.Context, string, string, string) error { return nil }
func (emptyL2) Revoke(context.Context, string) error                                 { return nil }
func (emptyL2) RevokeMany(context.Context, ...string) error                          { return nil }
func (emptyL2) Close()                                                               {}
