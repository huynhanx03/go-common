package request

import (
	"context"
	"sync"
)

// FanoutResult holds the outcome of a single concurrent task.
type FanoutResult[T any] struct {
	Value T
	Err   error
}

// Fanout executes multiple functions concurrently with a shared context.
// If any function returns an error, other functions continue to run
// (use context cancellation for early abort).
// Results are returned in the same order as the input functions.
func Fanout[T any](ctx context.Context, fns ...func(ctx context.Context) (T, error)) []FanoutResult[T] {
	results := make([]FanoutResult[T], len(fns))
	var wg sync.WaitGroup
	wg.Add(len(fns))

	for i, fn := range fns {
		go func(idx int, f func(ctx context.Context) (T, error)) {
			defer wg.Done()
			val, err := f(ctx)
			results[idx] = FanoutResult[T]{Value: val, Err: err}
		}(i, fn)
	}

	wg.Wait()
	return results
}

// FanoutFirst executes multiple functions concurrently and returns
// the first successful result. If all fail, returns the last error.
func FanoutFirst[T any](ctx context.Context, fns ...func(ctx context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type outcome struct {
		value T
		err   error
	}

	ch := make(chan outcome, len(fns))
	for _, fn := range fns {
		go func(f func(ctx context.Context) (T, error)) {
			val, err := f(ctx)
			ch <- outcome{value: val, err: err}
		}(fn)
	}

	var lastErr error
	for range fns {
		res := <-ch
		if res.err == nil {
			cancel()
			return res.value, nil
		}
		lastErr = res.err
	}

	var zero T
	return zero, lastErr
}

// FanoutCollect executes functions concurrently and collects only successful results.
// Errors are silently discarded — use Fanout if you need per-task error handling.
func FanoutCollect[T any](ctx context.Context, fns ...func(ctx context.Context) (T, error)) []T {
	results := Fanout(ctx, fns...)
	collected := make([]T, 0, len(results))
	for _, r := range results {
		if r.Err == nil {
			collected = append(collected, r.Value)
		}
	}
	return collected
}
