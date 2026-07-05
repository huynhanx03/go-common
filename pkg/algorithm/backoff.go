package algorithm

import (
	"math"
	"math/rand"
	"time"
)

// Backoff defaults.
const (
	defaultInitialDelay = 100 * time.Millisecond
	defaultMaxDelay     = 30 * time.Second
	defaultMultiplier   = 2.0
)

// Backoff computes the delay for a given retry attempt.
// Implementations must be safe for concurrent use.
type Backoff interface {
	// Delay returns how long to wait before the nth retry (0-indexed).
	Delay(attempt int) time.Duration
}

// -- Constant Backoff --

// ConstantBackoff returns the same delay for every attempt.
type ConstantBackoff struct {
	delay time.Duration
}

// NewConstantBackoff creates a constant backoff with the given delay.
func NewConstantBackoff(delay time.Duration) *ConstantBackoff {
	if delay <= 0 {
		delay = defaultInitialDelay
	}
	return &ConstantBackoff{delay: delay}
}

func (b *ConstantBackoff) Delay(_ int) time.Duration {
	return b.delay
}

// -- Linear Backoff --

// LinearBackoff increases delay linearly: initial * (attempt + 1), capped at max.
type LinearBackoff struct {
	initial time.Duration
	max     time.Duration
}

// NewLinearBackoff creates a linear backoff.
func NewLinearBackoff(initial, max time.Duration) *LinearBackoff {
	if initial <= 0 {
		initial = defaultInitialDelay
	}
	if max <= 0 {
		max = defaultMaxDelay
	}
	return &LinearBackoff{initial: initial, max: max}
}

func (b *LinearBackoff) Delay(attempt int) time.Duration {
	d := b.initial * time.Duration(attempt+1)
	if d > b.max {
		return b.max
	}
	return d
}

// -- Exponential Backoff --

// ExponentialBackoff increases delay exponentially: initial * multiplier^attempt, capped at max.
type ExponentialBackoff struct {
	initial    time.Duration
	max        time.Duration
	multiplier float64
}

// NewExponentialBackoff creates an exponential backoff.
func NewExponentialBackoff(initial, max time.Duration, multiplier float64) *ExponentialBackoff {
	if initial <= 0 {
		initial = defaultInitialDelay
	}
	if max <= 0 {
		max = defaultMaxDelay
	}
	if multiplier <= 1 {
		multiplier = defaultMultiplier
	}
	return &ExponentialBackoff{initial: initial, max: max, multiplier: multiplier}
}

func (b *ExponentialBackoff) Delay(attempt int) time.Duration {
	d := float64(b.initial) * math.Pow(b.multiplier, float64(attempt))
	if d > float64(b.max) {
		return b.max
	}
	return time.Duration(d)
}

// -- Jitter Backoff --

// JitterBackoff wraps any Backoff and adds random jitter to prevent thundering herd.
// The actual delay is randomized in [delay/2, delay].
type JitterBackoff struct {
	inner Backoff
}

// NewJitterBackoff wraps an existing backoff with random jitter.
func NewJitterBackoff(inner Backoff) *JitterBackoff {
	return &JitterBackoff{inner: inner}
}

func (b *JitterBackoff) Delay(attempt int) time.Duration {
	d := b.inner.Delay(attempt)
	half := d / 2
	jitter := time.Duration(rand.Int63n(int64(half) + 1))
	return half + jitter
}

// -- Convenience --

// DefaultExponentialBackoff creates an exponential backoff with jitter using sensible defaults.
// 100ms → 200ms → 400ms → 800ms ... capped at 30s, with ±50% jitter.
func DefaultExponentialBackoff() Backoff {
	return NewJitterBackoff(
		NewExponentialBackoff(defaultInitialDelay, defaultMaxDelay, defaultMultiplier),
	)
}
