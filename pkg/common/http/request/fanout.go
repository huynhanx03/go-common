package request

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

const (
	maxFanoutConcurrency = 256
	maxFanoutTasks       = 10_000
)

type FanoutMode uint8

const (
	FanoutAll FanoutMode = iota + 1
	FanoutFirstSuccess
	FanoutFirstError
)

type FanoutOptions struct {
	MaxConcurrency int
	Mode           FanoutMode
}

type FanoutResult[T any] struct {
	Value   T
	Err     error
	Started bool
}

// Fanout executes a copied task list through a fixed-size worker group.
// Results always correspond to input indexes.
func Fanout[T any](
	ctx context.Context,
	options FanoutOptions,
	tasks ...func(context.Context) (T, error),
) ([]FanoutResult[T], error) {
	if ctx == nil ||
		options.MaxConcurrency <= 0 ||
		options.MaxConcurrency > maxFanoutConcurrency ||
		(options.Mode != FanoutAll &&
			options.Mode != FanoutFirstSuccess &&
			options.Mode != FanoutFirstError) ||
		len(tasks) > maxFanoutTasks {
		return nil, ErrInvalidFanout
	}
	if err := ctx.Err(); err != nil {
		return make([]FanoutResult[T], len(tasks)), err
	}
	if len(tasks) == 0 {
		return []FanoutResult[T]{}, nil
	}

	copiedTasks := append([]func(context.Context) (T, error)(nil), tasks...)
	results := make([]FanoutResult[T], len(copiedTasks))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var next atomic.Int64
	var firstError error
	var firstErrorOnce sync.Once
	var successFound atomic.Bool
	group, _ := errgroup.WithContext(runCtx)
	workers := min(options.MaxConcurrency, len(copiedTasks))
	for range workers {
		group.Go(func() error {
			for {
				if runCtx.Err() != nil {
					return nil
				}
				index := int(next.Add(1) - 1)
				if index >= len(copiedTasks) {
					return nil
				}

				value, err := runFanoutTask(runCtx, copiedTasks[index])
				results[index] = FanoutResult[T]{
					Value:   value,
					Err:     err,
					Started: true,
				}
				switch options.Mode {
				case FanoutFirstError:
					if err != nil {
						firstErrorOnce.Do(func() {
							firstError = err
							cancel()
						})
					}
				case FanoutFirstSuccess:
					if err == nil && successFound.CompareAndSwap(false, true) {
						cancel()
					}
				}
			}
		})
	}
	_ = group.Wait()

	unstartedError := context.Canceled
	if ctx.Err() != nil {
		unstartedError = ctx.Err()
	}
	for index := range results {
		if !results[index].Started {
			results[index].Err = unstartedError
		}
	}

	if ctx.Err() != nil {
		return results, ctx.Err()
	}
	switch options.Mode {
	case FanoutFirstError:
		return results, firstError
	case FanoutFirstSuccess:
		if successFound.Load() {
			return results, nil
		}
		failures := make([]error, 0, len(results)+1)
		failures = append(failures, ErrNoSuccessfulResult)
		for _, result := range results {
			if result.Err != nil {
				failures = append(failures, result.Err)
			}
		}
		return results, errors.Join(failures...)
	default:
		return results, nil
	}
}

func runFanoutTask[T any](
	ctx context.Context,
	task func(context.Context) (T, error),
) (value T, err error) {
	if task == nil {
		return value, ErrInvalidTask
	}
	defer func() {
		if recover() != nil {
			var zero T
			value = zero
			err = &TaskPanicError{}
		}
	}()
	return task(ctx)
}
