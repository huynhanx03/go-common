package algorithm

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenBucketSpendsRefillsAndResets(t *testing.T) {
	var now atomic.Int64
	bucket := NewTokenBucket(WithBucketCapacity(3), WithBucketFillRate(2, time.Second), WithBucketClock(now.Load))
	if bucket.Allow(4) || bucket.Tokens() != 3 {
		t.Fatal("unaffordable request consumed tokens")
	}
	if !bucket.Allow(2) || !bucket.AllowOne() || bucket.AllowOne() {
		t.Fatal("admission did not respect the three-token budget")
	}
	now.Store(int64(250 * time.Millisecond))
	if bucket.Tokens() != 0.5 || bucket.AllowOne() {
		t.Fatal("partial refill granted a full token")
	}
	now.Store(int64(500 * time.Millisecond))
	if !bucket.AllowOne() || bucket.Tokens() != 0 {
		t.Fatal("fractional refill credit was lost")
	}
	now.Store(int64(10 * time.Second))
	if bucket.Tokens() != 3 {
		t.Fatal("refill exceeded capacity")
	}
	if !bucket.Allow(3) {
		t.Fatal("full bucket rejected capacity")
	}
	bucket.Reset()
	if !bucket.Allow(3) || bucket.AllowOne() {
		t.Fatal("reset did not restore exactly capacity")
	}
	now.Store(int64(9 * time.Second))
	if bucket.Tokens() != 0 {
		t.Fatal("backwards clock created tokens")
	}
}

func TestTokenBucketRejectsNonPositiveCosts(t *testing.T) {
	bucket := NewTokenBucket(WithBucketCapacity(3), WithBucketClock(func() int64 { return 0 }))
	for _, cost := range []int{0, -1, math.MinInt} {
		if bucket.Allow(cost) || bucket.Tokens() != 3 {
			t.Fatalf("Allow(%d) admitted an invalid cost or changed the balance", cost)
		}
	}
}

func TestSlidingWindowWeightsHistoryAndExpiresIt(t *testing.T) {
	var now atomic.Int64
	window := NewSlidingWindow(WithWindowLimit(4), WithWindowSize(10*time.Second), WithWindowClock(now.Load))
	for range 4 {
		if !window.Allow() {
			t.Fatal("rejected a request within the limit")
		}
	}
	if window.Allow() || window.Count() != 4 {
		t.Fatal("denied request changed the count")
	}
	now.Store(int64(10 * time.Second))
	if window.Count() != 4 || window.Allow() {
		t.Fatal("exact window boundary erased the previous window")
	}
	now.Store(int64(15 * time.Second))
	if window.Count() != 2 || !window.Allow() || !window.Allow() || window.Allow() {
		t.Fatal("half-window overlap did not leave exactly two admissions")
	}
	now.Store(int64(30 * time.Second))
	if window.Count() != 0 || !window.Allow() {
		t.Fatal("multiple elapsed windows retained stale history")
	}
	window.Reset()
	if window.Count() != 0 || !window.Allow() {
		t.Fatal("reset retained admission history")
	}
}

func TestRateLimitersPreserveDefaultsForInvalidOptions(t *testing.T) {
	var now atomic.Int64
	bucket := NewTokenBucket(WithBucketCapacity(-1), WithBucketFillRate(0, 0), WithBucketClock(nil), WithBucketClock(now.Load))
	if !bucket.Allow(10) || bucket.AllowOne() {
		t.Fatal("invalid options replaced the default bucket capacity")
	}
	now.Store(int64(time.Second))
	if !bucket.AllowOne() || bucket.AllowOne() {
		t.Fatal("invalid options replaced the default refill rate")
	}
	window := NewSlidingWindow(WithWindowLimit(0), WithWindowSize(-1), WithWindowClock(nil), WithWindowClock(now.Load))
	for range 60 {
		if !window.Allow() {
			t.Fatal("invalid options changed the default window limit")
		}
	}
	if window.Allow() {
		t.Fatal("default window exceeded its limit")
	}
	now.Store(int64(2 * time.Minute))
	if !window.Allow() {
		t.Fatal("default window failed to expire")
	}
}

func TestRateLimitersDoNotOverspendUnderContention(t *testing.T) {
	const limit = 16
	for name, allow := range map[string]func() bool{
		"token bucket":   NewTokenBucket(WithBucketCapacity(limit), WithBucketClock(func() int64 { return 0 })).AllowOne,
		"sliding window": NewSlidingWindow(WithWindowLimit(limit), WithWindowClock(func() int64 { return 0 })).Allow,
	} {
		t.Run(name, func(t *testing.T) {
			var admitted atomic.Int64
			var workers sync.WaitGroup
			start := make(chan struct{})
			for range 128 {
				workers.Go(func() {
					<-start
					if allow() {
						admitted.Add(1)
					}
				})
			}
			close(start)
			workers.Wait()
			if admitted.Load() != limit {
				t.Fatalf("admitted %d requests, want %d", admitted.Load(), limit)
			}
		})
	}
}
