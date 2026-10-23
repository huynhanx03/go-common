package cache

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
)

type readOnceL2 struct {
	*memoryL2
	key   string
	data  []byte
	calls atomic.Int32
}

func (m *readOnceL2) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if key == m.key && m.calls.Add(1) == 1 {
		return nil, false, nil
	}
	if key == m.key {
		return append([]byte(nil), m.data...), true, nil
	}
	return m.memoryL2.Get(ctx, key)
}

func TestFetchValidationAndLoaderOutcomes(t *testing.T) {
	c := newTestCache(t, newMemoryL1(), nil)
	defer c.Close()
	for _, tc := range []struct {
		key string
		ttl time.Duration
	}{{"", time.Minute}, {"x", 0}, {"x", -time.Second}, {string(make([]byte, MaxKeyBytes+1)), time.Minute}} {
		if _, err := Fetch[int](context.Background(), c, tc.key, tc.ttl, func(context.Context) (int, error) { return 0, nil }); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("Fetch invalid input = %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fetch[int](ctx, c, "key", time.Minute, func(context.Context) (int, error) { return 0, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Fetch = %v", err)
	}
	wantErr := errors.New("loader failed")
	if _, err := Fetch[int](context.Background(), c, "key", time.Minute, func(context.Context) (int, error) { return 0, wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("loader error = %v", err)
	}
}

func TestFetchFallsBackForCorruptAndExpiredL2Entries(t *testing.T) {
	for name, data := range map[string][]byte{
		"malformed": []byte("{"),
		"expired": func() []byte {
			b, _ := encodeEntry(entry[int]{Value: 1, ExpireAt: time.Now().Add(-time.Minute).UnixMilli()})
			return b
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			l2 := newMemoryL2()
			l2.versions["key"] = "v1"
			l2.values["key"] = data
			c := newTestCache(t, newMemoryL1(), l2)
			defer c.Close()
			got, err := Fetch[int](context.Background(), c, "key", time.Minute, func(context.Context) (int, error) { return 7, nil })
			if err != nil || got != 7 {
				t.Fatalf("Fetch fallback = %d, %v", got, err)
			}
		})
	}
	l2 := newMemoryL2()
	l2.errors["get"] = errors.New("backend unavailable")
	c := newTestCache(t, newMemoryL1(), l2)
	defer c.Close()
	if got, err := Fetch[int](context.Background(), c, "key", time.Minute, func(context.Context) (int, error) { return 8, nil }); err != nil || got != 8 {
		t.Fatalf("Fetch backend fallback = %d, %v", got, err)
	}
}

func TestFetchRechecksPositiveAndNegativeEntriesBeforeLoading(t *testing.T) {
	encoded, err := encodeEntry(newEntry("ready", time.Minute, 0))
	if err != nil {
		t.Fatal(err)
	}
	l2 := &readOnceL2{memoryL2: newMemoryL2(), key: "key", data: encoded}
	l2.versions["key"] = "v1"
	c := newTestCache(t, newMemoryL1(), l2)
	defer c.Close()
	if got, err := Fetch[string](context.Background(), c, "key", time.Minute, func(context.Context) (string, error) { t.Fatal("loader ran after positive recheck"); return "", nil }); err != nil || got != "ready" {
		t.Fatalf("positive recheck = %q, %v", got, err)
	}

	l1 := &delayedNegativeL1{memoryL1: newMemoryL1(), negativeKey: "missing" + NegativeKeySuffix}
	l1.values[l1.negativeKey] = entry[struct{}]{Negative: true, ExpireAt: time.Now().Add(time.Minute).UnixMilli()}
	c2 := newTestCache(t, l1, nil)
	defer c2.Close()
	if _, err := Fetch[int](context.Background(), c2, "missing", time.Minute, func(context.Context) (int, error) { t.Fatal("loader ran after negative recheck"); return 0, nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("negative recheck = %v", err)
	}
}

func TestReadRejectsInvalidEntriesAndPromotesL2NegativeEntries(t *testing.T) {
	ctx := context.Background()
	l1 := newMemoryL1()
	c := newTestCache(t, l1, nil)
	defer c.Close()
	f := fetcher[string]{cache: c, key: "key"}
	for name, value := range map[string]any{"wrong type": "value", "negative": entry[string]{Negative: true, ExpireAt: time.Now().Add(time.Minute).UnixMilli()}, "expired": entry[string]{ExpireAt: time.Now().Add(-time.Minute).UnixMilli()}} {
		l1.values["key"] = value
		if _, ok := f.read(ctx); ok {
			t.Errorf("read accepted %s value", name)
		}
		delete(l1.values, "key")
	}

	l2 := newMemoryL2()
	l2.versions["absent"] = "v1"
	negative, err := encodeNegativeEntry(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	l2.values["absent"+NegativeKeySuffix] = negative
	c2 := newTestCache(t, newMemoryL1(), l2)
	defer c2.Close()
	f2 := fetcher[int]{cache: c2, key: "absent"}
	if !f2.readNegative(ctx) || !f2.readNegative(ctx) {
		t.Fatal("L2 or promoted L1 negative entry was missed")
	}
	for name, data := range map[string][]byte{"malformed": []byte("{"), "positive": func() []byte {
		b, _ := encodeEntry(entry[struct{}]{ExpireAt: time.Now().Add(time.Minute).UnixMilli()})
		return b
	}(), "expired": func() []byte {
		b, _ := encodeEntry(entry[struct{}]{ExpireAt: time.Now().Add(-time.Minute).UnixMilli(), Negative: true})
		return b
	}()} {
		delete(c2.tiers.l1.(*memoryL1).values, "absent"+NegativeKeySuffix)
		l2.values["absent"+NegativeKeySuffix] = data
		if f2.readNegative(ctx) {
			t.Errorf("readNegative accepted %s entry", name)
		}
	}
}

func TestSingleflightCancellationAndLoaderErrors(t *testing.T) {
	var group singleflight.Group
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = doTypedContext[int](context.Background(), &group, "key", func() (int, error) {
			close(started)
			<-release
			return 1, nil
		})
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := doTypedContext[int](ctx, &group, "key", func() (int, error) { return 0, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter = %v", err)
	}
	close(release)
	<-done
	var failed singleflight.Group
	if _, err := doTypedContext[int](context.Background(), &failed, "failed", func() (int, error) { return 0, ErrNotFound }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("singleflight error = %v", err)
	}
}

func TestRefreshLifecycleAndXFetch(t *testing.T) {
	base := context.Background()
	detached, cancel := detachedContext(base)
	if detached.Err() != nil {
		t.Fatalf("detached error = %v", detached.Err())
	}
	cancel()
	withDeadline, stop := context.WithTimeout(base, time.Second)
	defer stop()
	detached, cancel = detachedContext(withDeadline)
	defer cancel()
	if _, ok := detached.Deadline(); !ok {
		t.Fatal("detached context lost deadline")
	}
	if jitterTTL(0) != 0 || jitterTTL(-time.Second) != -time.Second {
		t.Fatal("non-positive TTL was changed")
	}
	if v := jitterTTL(time.Second); v < 900*time.Millisecond || v > 1100*time.Millisecond {
		t.Fatalf("jittered TTL = %v", v)
	}
	if !(entry[int]{ExpireAt: time.Now().Add(-time.Second).UnixMilli()}).shouldRefresh() {
		t.Fatal("expired value should refresh")
	}
	if (entry[int]{ExpireAt: time.Now().Add(time.Hour).UnixMilli()}).shouldRefresh() {
		t.Fatal("fresh value unexpectedly refreshes")
	}
	for range cap(refreshSlots) {
		refreshSlots <- struct{}{}
	}
	if refreshAsync(new(singleflight.Group), "full", func() (int, error) { return 0, nil }) {
		t.Fatal("refresh exceeded concurrency limit")
	}
	for range cap(refreshSlots) {
		<-refreshSlots
	}
	done := make(chan struct{})
	if !refreshAsync(new(singleflight.Group), "free", func() (int, error) { close(done); return 0, nil }) {
		t.Fatal("refresh rejected free slot")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh did not run")
	}
	deadline := time.Now().Add(time.Second)
	for len(refreshSlots) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(refreshSlots) != 0 {
		t.Fatal("refresh slot was not released")
	}
}

func TestFetchRefreshReturnsStaleValueAndHonorsCapacity(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprintf("capacity_full_%t", full), func(t *testing.T) {
			l1 := newMemoryL1()
			l1.versions["key"] = "v1"
			l1.values["key"] = entry[int]{Value: 1, ExpireAt: time.Now().Add(time.Second).UnixMilli(), DeltaMs: 1_000_000_000_000}
			c := newTestCache(t, l1, nil)
			defer c.Close()
			if full {
				for range cap(refreshSlots) {
					refreshSlots <- struct{}{}
				}
				t.Cleanup(func() {
					for range cap(refreshSlots) {
						<-refreshSlots
					}
				})
			}
			started := make(chan struct{})
			got, err := Fetch[int](context.Background(), c, "key", time.Minute, func(context.Context) (int, error) { close(started); return 2, nil })
			if err != nil || got != 1 {
				t.Fatalf("Fetch stale = %d, %v", got, err)
			}
			if full {
				select {
				case <-started:
					t.Fatal("loader ran with full refresh slots")
				default:
				}
				return
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("background loader did not run")
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				value, ok := l1.Get(context.Background(), "key")
				if cached, valid := value.(entry[int]); ok && valid && cached.Value == 2 {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("background refresh did not replace stale value")
		})
	}
}

func TestLoadDoesNotCacheAfterContextCancellation(t *testing.T) {
	l1 := newMemoryL1()
	c := newTestCache(t, l1, nil)
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	f := fetcher[int]{cache: c, key: "key", ttl: time.Minute, loader: func(context.Context) (int, error) { cancel(); return 9, nil }}
	if _, err := f.load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("load = %v", err)
	}
	if len(l1.values) != 0 {
		t.Fatalf("canceled load cached: %v", l1.values)
	}
}
