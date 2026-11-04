package cache

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheFetchAndInvalidationAcrossConfiguredTiers(t *testing.T) {
	ctx := context.Background()
	l1, l2 := newMemoryL1(), newMemoryL2()
	c := newTestCache(t, l1, l2)
	defer c.Close()
	var loads atomic.Int32
	loader := func(context.Context) (string, error) { return fmt.Sprintf("value-%d", loads.Add(1)), nil }

	first, err := For[string](c).Fetch(ctx, "profile", time.Minute, loader)
	if err != nil || first != "value-1" {
		t.Fatalf("typed Fetch = %q, %v", first, err)
	}
	second, err := Fetch[string](ctx, c, "profile", time.Minute, loader)
	if err != nil || second != first {
		t.Fatalf("Fetch hit = %q, %v; want %q", second, err, first)
	}
	if len(l2.values) != 1 {
		t.Fatalf("L2 entries = %d, want 1", len(l2.values))
	}

	if err := c.Delete(ctx, "profile"); err != nil {
		t.Fatal(err)
	}
	third, err := Fetch[string](ctx, c, "profile", time.Minute, loader)
	if err != nil || third != "value-2" {
		t.Fatalf("Fetch after Delete = %q, %v", third, err)
	}
	if loads.Load() != 2 {
		t.Fatalf("loader calls = %d, want 2", loads.Load())
	}
	if err := c.DeleteAll(ctx, "profile", "profile", "other"); err != nil {
		t.Fatal(err)
	}
}

func TestCacheSupportsL2PromotionAndRawBytes(t *testing.T) {
	ctx := context.Background()
	l1, l2 := newMemoryL1(), newMemoryL2()
	cached := newEntry("from-l2", time.Minute, time.Second)
	data, err := encodeEntry(cached)
	if err != nil {
		t.Fatal(err)
	}
	l2.versions["item"] = "v1"
	l2.values["item"] = data
	c := newTestCache(t, l1, l2)
	defer c.Close()
	got, err := Fetch[string](ctx, c, "item", time.Minute, func(context.Context) (string, error) { t.Fatal("loader called on L2 hit"); return "", nil })
	if err != nil || got != "from-l2" {
		t.Fatalf("L2 Fetch = %q, %v", got, err)
	}
	if _, ok := l1.values["item"]; !ok {
		t.Fatal("L2 value was not promoted to L1")
	}

	rawL2 := newMemoryL2()
	bytesData, err := encodeEntry(newEntry([]byte{0, 1, 255}, time.Minute, 0))
	if err != nil {
		t.Fatal(err)
	}
	rawL2.versions["blob"] = "v1"
	rawL2.values["blob"] = bytesData
	rawCache := newTestCache(t, nil, rawL2)
	defer rawCache.Close()
	blob, err := Fetch[[]byte](ctx, rawCache, "blob", time.Minute, func(context.Context) ([]byte, error) { t.Fatal("loader called for raw L2 hit"); return nil, nil })
	if err != nil || len(blob) != 3 || blob[2] != 255 {
		t.Fatalf("raw L2 Fetch = %v, %v", blob, err)
	}
}

func TestCacheNegativeEntriesPreventRepeatedLoads(t *testing.T) {
	c := newTestCache(t, newMemoryL1(), newMemoryL2())
	defer c.Close()
	var loads atomic.Int32
	loader := func(context.Context) (int, error) { loads.Add(1); return 0, ErrNotFound }
	for range 2 {
		if _, err := Fetch[int](context.Background(), c, "missing", time.Minute, loader); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Fetch missing error = %v", err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", loads.Load())
	}
}

func TestCacheRejectsInvalidOperationsAndJoinsTierErrors(t *testing.T) {
	ctx := context.Background()
	l1, l2 := newMemoryL1(), newMemoryL2()
	c := newTestCache(t, l1, l2)
	defer c.Close()
	for _, key := range []string{"", string(make([]byte, MaxKeyBytes+1))} {
		if err := c.Delete(ctx, key); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("Delete(%d-byte key) = %v", len(key), err)
		}
	}
	if err := c.DeleteAll(ctx, "", "valid"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("DeleteAll invalid key = %v", err)
	}
	if err := c.DeleteAll(ctx); err != nil {
		t.Fatalf("DeleteAll empty = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Delete(canceled, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete canceled = %v", err)
	}
	if err := c.DeleteAll(canceled, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteAll canceled = %v", err)
	}

	l1.errors["revoke"] = errors.New("local revoke")
	l2.errors["revoke"] = errors.New("remote revoke")
	if err := c.Delete(ctx, "key"); !errors.Is(err, l1.errors["revoke"]) || !errors.Is(err, l2.errors["revoke"]) {
		t.Fatalf("Delete errors = %v", err)
	}
	l1.errors["revoke_many"] = errors.New("local batch revoke")
	l2.errors["revoke_many"] = errors.New("remote batch revoke")
	if err := c.DeleteAll(ctx, "key"); err == nil {
		t.Fatal("DeleteAll hid tier errors")
	}
}

func TestCacheConstructionAndCloseForEachTierMode(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New empty config = %v", err)
	}
	if _, err := New(Config{L1: newMemoryL1()}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New without logger = %v", err)
	}
	l1 := newMemoryL1()
	c := newTestCache(t, l1, nil)
	c.Close()
	c.Close()
	if l1.closed.Load() != 1 {
		t.Fatalf("L1 close calls = %d", l1.closed.Load())
	}
	l2 := newMemoryL2()
	c = newTestCache(t, nil, l2)
	c.Close()
	if l2.closed.Load() != 1 {
		t.Fatalf("L2 close calls = %d", l2.closed.Load())
	}
}
