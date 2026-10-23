package algorithm

import (
	"errors"
	"math"
	"math/rand"
	"time"

	"github.com/huynhanx03/go-common/pkg/utils"
)

var ErrInvalidBackoff = errors.New("invalid backoff policy")

// BackoffJitter returns a non-negative amount added to an exponential delay.
type BackoffJitter func(time.Duration) time.Duration

// CalculateBackoff returns a checked, saturating exponential delay.
func CalculateBackoff(attempt int, baseDelay, maxDelay time.Duration, jitter BackoffJitter) (time.Duration, error) {
	if attempt < 0 || baseDelay <= 0 || maxDelay < baseDelay {
		return 0, ErrInvalidBackoff
	}
	delay := baseDelay
	for index := 0; index < attempt && delay < maxDelay; index++ {
		if delay > maxDelay/2 {
			delay = maxDelay
			break
		}
		delay *= 2
	}
	if delay > maxDelay {
		delay = maxDelay
	}
	if jitter == nil || delay == maxDelay {
		return delay, nil
	}
	extra := jitter(delay)
	if extra < 0 {
		return 0, ErrInvalidBackoff
	}
	if extra > maxDelay-delay {
		return maxDelay, nil
	}
	return delay + extra, nil
}

func defaultBackoffJitter(delay time.Duration) time.Duration {
	return utils.JitterDurationRange(delay, 0, 0.1, rand.Float64()) - delay
}

// CalculateBackoffByTime calculates backoff capped by a maximum duration.
// Deprecated: use CalculateBackoff and handle invalid configuration.
func CalculateBackoffByTime(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	delay, err := CalculateBackoff(attempt, baseDelay, maxDelay, defaultBackoffJitter)
	if err != nil {
		return 0
	}
	return delay
}

// CalculateBackoffByAttempt calculates backoff capped by a maximum attempt.
// Deprecated: use the retry policy in pkg/common/http/request.
func CalculateBackoffByAttempt(attempt int, baseDelay time.Duration, maxAttempts int) time.Duration {
	if maxAttempts < 0 || baseDelay <= 0 {
		return 0
	}
	maxDelay := baseDelay
	for index := 0; index < maxAttempts; index++ {
		if maxDelay > time.Duration(1<<63-1)/2 {
			maxDelay = time.Duration(1<<63 - 1)
			break
		}
		maxDelay *= 2
	}
	delay, err := CalculateBackoff(min(attempt, maxAttempts), baseDelay, maxDelay, defaultBackoffJitter)
	if err != nil {
		return 0
	}
	return delay
}

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
	return utils.JitterDurationRange(d, 0.5, 0, rand.Float64())
}

// -- Convenience --

// DefaultExponentialBackoff creates an exponential backoff with jitter using sensible defaults.
// 100ms → 200ms → 400ms → 800ms ... capped at 30s, with each delay randomized to [50%, 100%].
func DefaultExponentialBackoff() Backoff {
	return NewJitterBackoff(
		NewExponentialBackoff(defaultInitialDelay, defaultMaxDelay, defaultMultiplier),
	)
}
