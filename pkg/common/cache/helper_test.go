package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

// fakeLocal is a minimal LocalCache for tests (no eviction, coarse TTL).
type fakeLocal struct {
	mu sync.RWMutex
	m  map[string]any
}

func newFakeLocal() *fakeLocal { return &fakeLocal{m: make(map[string]any)} }

func (f *fakeLocal) Get(key string) (any, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	v, ok := f.m[key]
	return v, ok
}
func (f *fakeLocal) Set(key string, value any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = value
	return true
}
func (f *fakeLocal) SetWithTTL(key string, value any, _ time.Duration) bool {
	return f.Set(key, value)
}
func (f *fakeLocal) Delete(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, key)
}
func (f *fakeLocal) Clear()      { f.mu.Lock(); defer f.mu.Unlock(); f.m = make(map[string]any) }
func (f *fakeLocal) Close()      {}
func (f *fakeLocal) Stats() Stats { return Stats{} }

var _ LocalCache[string, any] = (*fakeLocal)(nil)

// fakeEngine is a minimal CacheEngine for tests: Get/Set/Delete over a map.
// Unused interface methods come from the nil embedded interface (panic if hit).
type fakeEngine struct {
	CacheEngine
	mu sync.RWMutex
	m  map[string][]byte
}

func newFakeEngine() *fakeEngine { return &fakeEngine{m: make(map[string][]byte)} }

func (f *fakeEngine) Get(_ context.Context, key string) ([]byte, bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if v, ok := f.m[key]; ok {
		return v, true, nil
	}
	return nil, false, ErrKeyNotFound
}
func (f *fakeEngine) Set(_ context.Context, key string, value any, _ time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = data
	return nil
}
func (f *fakeEngine) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, key)
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestFetchWithRefreshLocal(t *testing.T) {
	c := newFakeLocal()
	sf := &singleflight.Group{}
	var calls atomic.Int64

	fn := func() (int64, error) { return calls.Add(1), nil }

	// Miss → loads once.
	v, err := FetchWithRefresh(c, sf, "k", 150*time.Millisecond, fn)
	if err != nil || v != 1 {
		t.Fatalf("first fetch = %d, %v", v, err)
	}

	// Fresh hit (instant fn → delta ~0 → refresh probability ~0).
	if v, _ := FetchWithRefresh(c, sf, "k", 150*time.Millisecond, fn); v != 1 {
		t.Fatalf("fresh hit = %d, want 1", v)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}

	// Past expiry (fakeLocal never evicts): hit serves the stale value
	// immediately and refreshes in background — XFetch fires with p=1.
	time.Sleep(200 * time.Millisecond)
	if v, _ := FetchWithRefresh(c, sf, "k", 150*time.Millisecond, fn); v != 1 {
		t.Fatalf("stale hit = %d, want 1 (stale served)", v)
	}
	waitFor(t, func() bool { return calls.Load() == 2 })

	// After refresh completes, hits serve the new value without reloading.
	waitFor(t, func() bool {
		v, _ := FetchWithRefresh(c, sf, "k", 150*time.Millisecond, fn)
		return v == 2
	})
}

func TestFetchRemoteWithRefresh(t *testing.T) {
	e := newFakeEngine()
	sf := &singleflight.Group{}
	ctx := context.Background()
	var calls atomic.Int64

	fn := func(context.Context) (int64, error) { return calls.Add(1), nil }

	v, err := FetchRemoteWithRefresh(ctx, e, sf, "k", 150*time.Millisecond, fn)
	if err != nil || v != 1 {
		t.Fatalf("first fetch = %d, %v", v, err)
	}

	if v, _ := FetchRemoteWithRefresh(ctx, e, sf, "k", 150*time.Millisecond, fn); v != 1 {
		t.Fatalf("fresh hit = %d, want 1", v)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}

	time.Sleep(200 * time.Millisecond)
	if v, _ := FetchRemoteWithRefresh(ctx, e, sf, "k", 150*time.Millisecond, fn); v != 1 {
		t.Fatalf("stale hit = %d, want 1 (stale served)", v)
	}
	waitFor(t, func() bool { return calls.Load() == 2 })

	waitFor(t, func() bool {
		v, _ := FetchRemoteWithRefresh(ctx, e, sf, "k", 150*time.Millisecond, fn)
		return v == 2
	})
}

func TestRemoteGetSetDelete(t *testing.T) {
	e := newFakeEngine()
	ctx := context.Background()

	type user struct {
		Name string `json:"name"`
	}

	if _, ok, err := GetRemote[user](ctx, e, "u"); ok || err != nil {
		t.Fatalf("miss = ok:%v err:%v, want clean miss", ok, err)
	}

	if err := SetRemote(ctx, e, "u", user{Name: "jerry"}, time.Minute); err != nil {
		t.Fatalf("SetRemote: %v", err)
	}
	u, ok, err := GetRemote[user](ctx, e, "u")
	if err != nil || !ok || u.Name != "jerry" {
		t.Fatalf("GetRemote = %+v, %v, %v", u, ok, err)
	}

	if err := DeleteRemote(ctx, e, "u"); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if _, ok, _ := GetRemote[user](ctx, e, "u"); ok {
		t.Fatal("key still present after DeleteRemote")
	}
}

func TestJitterTTL(t *testing.T) {
	ttl := 10 * time.Minute
	for i := 0; i < 1000; i++ {
		j := jitterTTL(ttl)
		if j < time.Duration(float64(ttl)*0.9) || j >= time.Duration(float64(ttl)*1.1) {
			t.Fatalf("jitterTTL(%v) = %v, outside ±10%%", ttl, j)
		}
	}
	if jitterTTL(0) != 0 {
		t.Error("jitterTTL(0) should stay 0")
	}
}

func TestFetchNegativeCachingLocal(t *testing.T) {
	c := newFakeLocal()
	sf := &singleflight.Group{}
	var calls atomic.Int64

	fn := func() (string, error) {
		calls.Add(1)
		return "", ErrNotFound
	}

	// First lookup hits the source and reports not-found.
	if _, err := Fetch(c, sf, "missing", time.Minute, fn); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	// Subsequent lookups are absorbed by the negative marker.
	for i := 0; i < 5; i++ {
		if _, err := Fetch(c, sf, "missing", time.Minute, fn); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("source called %d times, want 1", calls.Load())
	}
}

func TestFetchRemoteNegativeCaching(t *testing.T) {
	e := newFakeEngine()
	sf := &singleflight.Group{}
	ctx := context.Background()
	var calls atomic.Int64

	fn := func(context.Context) (string, error) {
		calls.Add(1)
		return "", ErrNotFound
	}

	if _, err := FetchRemote(ctx, e, sf, "missing", time.Minute, fn); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := FetchRemote(ctx, e, sf, "missing", time.Minute, fn); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("source called %d times, want 1", calls.Load())
	}

	// A real error (not ErrNotFound) must NOT be negative-cached.
	boom := errors.New("db down")
	fnErr := func(context.Context) (string, error) {
		calls.Add(1)
		return "", boom
	}
	if _, err := FetchRemote(ctx, e, sf, "other", time.Minute, fnErr); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if _, err := FetchRemote(ctx, e, sf, "other", time.Minute, fnErr); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom (retry reaches source)", err)
	}
}

func TestFetchRemoteBasic(t *testing.T) {
	e := newFakeEngine()
	sf := &singleflight.Group{}
	ctx := context.Background()
	var calls atomic.Int64

	fn := func(context.Context) (string, error) {
		calls.Add(1)
		return "data", nil
	}

	for i := 0; i < 3; i++ {
		v, err := FetchRemote(ctx, e, sf, "k", time.Minute, fn)
		if err != nil || v != "data" {
			t.Fatalf("FetchRemote = %q, %v", v, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}
