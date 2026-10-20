package utils

import (
	"errors"
	"math/rand"
	"time"
)

var ErrInvalidBackoff = errors.New("utils: invalid backoff policy")

// BackoffJitter returns a non-negative amount added to the exponential delay.
type BackoffJitter func(time.Duration) time.Duration

// CalculateBackoff returns a checked, saturating exponential delay.
func CalculateBackoff(
	attempt int,
	baseDelay,
	maxDelay time.Duration,
	jitter BackoffJitter,
) (time.Duration, error) {
	if attempt < 0 ||
		baseDelay <= 0 ||
		maxDelay < baseDelay {
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
	return time.Duration(rand.Float64() * float64(delay) * 0.1)
}

// CalculateBackoffByTime calculates backoff capped by a maximum duration.
// Deprecated: use CalculateBackoff and handle invalid configuration.
func CalculateBackoffByTime(
	attempt int,
	baseDelay,
	maxDelay time.Duration,
) time.Duration {
	delay, err := CalculateBackoff(
		attempt,
		baseDelay,
		maxDelay,
		defaultBackoffJitter,
	)
	if err != nil {
		return 0
	}
	return delay
}

// CalculateBackoffByAttempt calculates backoff capped by a maximum attempt.
// Deprecated: use the retry policy in pkg/common/http/request.
func CalculateBackoffByAttempt(
	attempt int,
	baseDelay time.Duration,
	maxAttempts int,
) time.Duration {
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
	delay, err := CalculateBackoff(
		min(attempt, maxAttempts),
		baseDelay,
		maxDelay,
		defaultBackoffJitter,
	)
	if err != nil {
		return 0
	}
	return delay
}
