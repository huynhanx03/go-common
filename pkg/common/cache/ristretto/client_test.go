package ristretto

import (
	"context"
	"sync"
	"testing"
	"time"
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

func TestCloseIsConcurrentIdempotentAndOperationsFailClosed(t *testing.T) {
	c, err := New[string, string]()
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			c.Close()
		}()
	}
	wait.Wait()
	if c.Set("late", "value") {
		t.Fatal("Set succeeded after Close")
	}
	if c.SetWithTTL(context.Background(), "late", "value", time.Minute) {
		t.Fatal("SetWithTTL succeeded after Close")
	}
	if _, found := c.Get(context.Background(), "late"); found {
		t.Fatal("Get found a value after Close")
	}
	c.Delete("late")
	c.Clear()
	if stats := c.Stats(); stats != (Stats{}) {
		t.Fatalf("Stats() after Close = %+v; want zero", stats)
	}
}

func TestStats(t *testing.T) {
	c := newTestCache(t)

	c.Set("k", "v")
	c.Get(context.Background(), "k")    // hit
	c.Get(context.Background(), "nope") // miss

	s := c.Stats()
	if s.Hits < 1 || s.Misses < 1 || s.KeyCount < 1 {
		t.Errorf("Stats = %+v, want hits/misses/keycount >= 1", s)
	}
}

func TestStatsWhenMetricsAreDisabled(t *testing.T) {
	c, err := New[string, any](WithMetrics(false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	if stats := c.Stats(); stats != (Stats{}) {
		t.Fatalf("Stats() with metrics disabled = %+v; want zero", stats)
	}
}
