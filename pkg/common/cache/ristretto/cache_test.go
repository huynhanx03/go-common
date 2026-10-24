package ristretto

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/logger"
)

func TestCacheFacadeUsesRistrettoAsL1(t *testing.T) {
	l1 := newTestCache(t)
	facade, err := cache.New(cache.Config{L1: l1, Logger: logger.Wrap(zap.NewNop())})
	if err != nil {
		t.Fatal(err)
	}
	var loads atomic.Int32
	loader := func(context.Context) (int, error) {
		return int(loads.Add(1)), nil
	}
	ctx := context.Background()
	for _, want := range []int{1, 1} {
		got, err := cache.Fetch(ctx, facade, "value", time.Minute, loader)
		if err != nil || got != want {
			t.Fatalf("Fetch() = %d, %v; want %d", got, err, want)
		}
	}
	if err := facade.Delete(ctx, "value"); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Fetch(ctx, facade, "value", time.Minute, loader)
	if err != nil || got != 2 {
		t.Fatalf("Fetch() after invalidation = %d, %v; want 2", got, err)
	}
	if loads.Load() != 2 {
		t.Fatalf("loader calls = %d, want 2", loads.Load())
	}
}

func TestSetGetDelete(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	if !c.Set("k", "v") {
		t.Fatal("Set returned false")
	}
	if v, ok := c.Get(ctx, "k"); !ok || v != "v" {
		t.Fatalf("Get = %v, %v", v, ok)
	}

	c.Delete("k")
	if _, ok := c.Get(ctx, "k"); ok {
		t.Fatal("key still present after Delete")
	}
}

func TestSetWithTTL(t *testing.T) {
	t.Run("rejects non-positive TTL", func(t *testing.T) {
		c := newTestCache(t)
		ctx := context.Background()
		for _, ttl := range []time.Duration{0, -time.Second} {
			if c.SetWithTTL(ctx, "k", "v", ttl) {
				t.Errorf("SetWithTTL(%s) succeeded", ttl)
			}
		}
		if _, ok := c.Get(ctx, "k"); ok {
			t.Fatal("invalid TTL stored a value")
		}
	})

	t.Run("expires the value", func(t *testing.T) {
		c := newTestCache(t)
		ctx := context.Background()
		if !c.SetWithTTL(ctx, "k", "v", 150*time.Millisecond) {
			t.Fatal("SetWithTTL returned false")
		}
		if _, ok := c.Get(ctx, "k"); !ok {
			t.Fatal("key missing right after SetWithTTL")
		}

		time.Sleep(500 * time.Millisecond)
		if _, ok := c.Get(ctx, "k"); ok {
			t.Fatal("key still present after TTL elapsed")
		}
	})
}

func TestGetAndSetWithTTLIgnoreCanceledContext(t *testing.T) {
	c := newTestCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, ok := c.Get(ctx, "key"); ok {
		t.Fatal("Get returned a value for a canceled context")
	}
	if c.SetWithTTL(ctx, "key", "value", time.Minute) {
		t.Fatal("SetWithTTL accepted a canceled context")
	}
}

func TestClear(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()

	if _, ok := c.Get(ctx, "a"); ok {
		t.Fatal("key a survived Clear")
	}
	if _, ok := c.Get(ctx, "b"); ok {
		t.Fatal("key b survived Clear")
	}
}

func TestGetRejectsUnexpectedStoredType(t *testing.T) {
	c, err := New[string, string]()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()
	c.inner.Set(dataCacheKey("key"), 42, defaultCost)
	c.inner.Wait()
	if _, ok := c.Get(ctx, "key"); ok {
		t.Fatal("Get accepted a value with the wrong type")
	}
}
