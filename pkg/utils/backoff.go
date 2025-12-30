package utils

import (
	"math"
	"math/rand"
	"time"
)

// CalculateBackoffByTime calculates backoff capped by a maximum duration.
func CalculateBackoffByTime(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	backoff := float64(baseDelay) * math.Pow(2, float64(attempt))

	// Cap by Max Duration
	if backoff > float64(maxDelay) {
		backoff = float64(maxDelay)
	}

	jitter := rand.Float64() * (backoff * 0.1)
	return time.Duration(backoff + jitter)
}

// CalculateBackoffByAttempt calculates backoff capped by a maximum attempt count.
func CalculateBackoffByAttempt(attempt int, baseDelay time.Duration, maxAttempts int) time.Duration {
	// Cap the exponent to avoid overflow or excessive delays
	if attempt > maxAttempts {
		attempt = maxAttempts
	}

	backoff := float64(baseDelay) * math.Pow(2, float64(attempt))
	jitter := rand.Float64() * (backoff * 0.1)
	return time.Duration(backoff + jitter)
}
