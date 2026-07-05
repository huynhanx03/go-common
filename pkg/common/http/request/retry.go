package request

import (
	"context"
	"time"

	"github.com/huynhanx03/go-common/pkg/algorithm"
)

// Retry defaults.
const (
	defaultMaxRetries = 3
)

// RetryConfig configures the retry behavior.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (excludes the initial call).
	MaxRetries int

	// Backoff computes the delay between retries.
	// Defaults to exponential backoff with jitter.
	Backoff algorithm.Backoff

	// ShouldRetry decides if the error is retryable.
	// Defaults to retrying all non-nil errors.
	ShouldRetry func(err error) bool
}

// RetryResult holds the outcome of a retried operation.
type RetryResult[T any] struct {
	Value    T
	Err      error
	Attempts int
}

// Retry executes fn up to MaxRetries+1 times, sleeping between attempts
// using the configured backoff strategy. Respects context cancellation.
func Retry[T any](ctx context.Context, cfg RetryConfig, fn func(ctx context.Context) (T, error)) RetryResult[T] {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.Backoff == nil {
		cfg.Backoff = algorithm.DefaultExponentialBackoff()
	}
	if cfg.ShouldRetry == nil {
		cfg.ShouldRetry = func(err error) bool { return err != nil }
	}

	var result RetryResult[T]

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		result.Attempts = attempt + 1

		val, err := fn(ctx)
		if err == nil {
			result.Value = val
			result.Err = nil
			return result
		}

		result.Err = err

		// Don't retry if the error is not retryable or we're out of attempts
		if !cfg.ShouldRetry(err) || attempt == cfg.MaxRetries {
			return result
		}

		// Wait with backoff, but respect context cancellation
		delay := cfg.Backoff.Delay(attempt)
		select {
		case <-ctx.Done():
			result.Err = ctx.Err()
			return result
		case <-time.After(delay):
		}
	}

	return result
}

// RetryVoid is a convenience wrapper for operations that return only an error.
func RetryVoid(ctx context.Context, cfg RetryConfig, fn func(ctx context.Context) error) error {
	result := Retry[struct{}](ctx, cfg, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return result.Err
}
