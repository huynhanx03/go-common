package algorithm

import (
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

// Token bucket defaults.
const (
	defaultBucketCapacity = 10
	defaultFillRate       = 1           // tokens per fill interval
	defaultFillInterval   = time.Second // refill once per second
	defaultTokensPerTake  = 1           // tokens consumed per Allow call
)

// TokenBucket implements the token bucket rate-limiting algorithm.
// It is safe for concurrent use.
type TokenBucket struct {
	mu       sync.Locker
	capacity float64
	tokens   float64
	fillRate float64 // tokens added per nanosecond
	lastFill int64
	now      func() int64 // injectable clock (unix nanoseconds)
}

// TokenBucketOption configures a TokenBucket.
type TokenBucketOption func(*TokenBucket)

// WithBucketCapacity sets the maximum number of tokens the bucket can hold.
func WithBucketCapacity(cap int) TokenBucketOption {
	return func(tb *TokenBucket) {
		if cap > 0 {
			tb.capacity = float64(cap)
			tb.tokens = float64(cap)
		}
	}
}

// WithBucketFillRate sets how many tokens are added per fill interval.
func WithBucketFillRate(tokens int, interval time.Duration) TokenBucketOption {
	return func(tb *TokenBucket) {
		if tokens > 0 && interval > 0 {
			tb.fillRate = float64(tokens) / float64(interval)
		}
	}
}

// WithBucketClock overrides the time source (unix nanoseconds).
func WithBucketClock(fn func() int64) TokenBucketOption {
	return func(tb *TokenBucket) {
		if fn != nil {
			tb.now = fn
		}
	}
}

// NewTokenBucket creates a token bucket with the given options.
func NewTokenBucket(opts ...TokenBucketOption) *TokenBucket {
	tb := &TokenBucket{
		mu:       locks.NewSpinLock(),
		capacity: defaultBucketCapacity,
		tokens:   defaultBucketCapacity,
		fillRate: float64(defaultFillRate) / float64(defaultFillInterval),
		now:      func() int64 { return time.Now().UnixNano() },
	}
	for _, opt := range opts {
		opt(tb)
	}
	tb.lastFill = tb.now()
	return tb
}

// Allow checks whether n tokens can be consumed.
// Returns true and consumes the tokens if available, false otherwise.
func (tb *TokenBucket) Allow(n int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	cost := float64(n)
	if tb.tokens < cost {
		return false
	}
	tb.tokens -= cost
	return true
}

// AllowOne is a convenience method equivalent to Allow(1).
func (tb *TokenBucket) AllowOne() bool {
	return tb.Allow(defaultTokensPerTake)
}

// Tokens returns the current number of available tokens.
func (tb *TokenBucket) Tokens() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	return tb.tokens
}

// Reset restores the bucket to full capacity.
func (tb *TokenBucket) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tokens = tb.capacity
	tb.lastFill = tb.now()
}

// refill adds tokens based on elapsed time since last refill.
func (tb *TokenBucket) refill() {
	now := tb.now()
	elapsed := now - tb.lastFill
	if elapsed <= 0 {
		return
	}

	tb.tokens += float64(elapsed) * tb.fillRate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.lastFill = now
}
