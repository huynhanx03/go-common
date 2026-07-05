package ristretto

import (
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
)

func newTestCache(t *testing.T) *Cache[string, any] {
	t.Helper()
	c, err := New[string, any]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestSetGetDelete(t *testing.T) {
	c := newTestCache(t)

	if !c.Set("k", "v") {
		t.Fatal("Set returned false")
	}
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Fatalf("Get = %v, %v", v, ok)
	}

	c.Delete("k")
	if _, ok := c.Get("k"); ok {
		t.Fatal("key still present after Delete")
	}
}

func TestSetWithTTLExpires(t *testing.T) {
	c := newTestCache(t)

	if !c.SetWithTTL("k", "v", 150*time.Millisecond) {
		t.Fatal("SetWithTTL returned false")
	}
	if _, ok := c.Get("k"); !ok {
		t.Fatal("key missing right after SetWithTTL")
	}

	time.Sleep(500 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("key still present after TTL elapsed")
	}
}

func TestClear(t *testing.T) {
	c := newTestCache(t)

	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()

	if _, ok := c.Get("a"); ok {
		t.Fatal("key a survived Clear")
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("key b survived Clear")
	}
}

func TestTypedGetViaHelper(t *testing.T) {
	c := newTestCache(t)

	cache.Set(c, "n", 42)
	if v, ok := cache.Get[int](c, "n"); !ok || v != 42 {
		t.Fatalf("cache.Get[int] = %v, %v", v, ok)
	}
	if _, ok := cache.Get[string](c, "n"); ok {
		t.Fatal("cache.Get[string] on int value reported ok")
	}
}

func TestStats(t *testing.T) {
	c := newTestCache(t)

	c.Set("k", "v")
	c.Get("k")    // hit
	c.Get("nope") // miss

	s := c.Stats()
	if s.Hits < 1 || s.Misses < 1 || s.KeyCount < 1 {
		t.Errorf("Stats = %+v, want hits/misses/keycount >= 1", s)
	}
}
