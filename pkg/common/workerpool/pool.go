// Package workerpool provides bounded, context-aware adapters backed by ants.
package workerpool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
)

const admissionPollInterval = time.Millisecond

type Pool struct {
	*ants.Pool
	nonblocking bool
	releaseMu   sync.Mutex
}

func NewPool(size int, options ...Option) (*Pool, error) {
	if size <= 0 || size > maxPoolSize {
		return nil, ErrInvalidPoolSize
	}
	vendor, normalized, err := loadBoundedOptions(options...)
	if err != nil {
		return nil, err
	}
	pool, err := ants.NewPool(size, vendor...)
	if err != nil {
		return nil, err
	}
	return &Pool{Pool: pool, nonblocking: normalized.Nonblocking}, nil
}

func (pool *Pool) Submit(task func()) error {
	return pool.SubmitContext(context.Background(), task)
}

func (pool *Pool) SubmitContext(ctx context.Context, task func()) error {
	if pool == nil || pool.Pool == nil || task == nil {
		return ErrInvalidTask
	}
	return admit(ctx, pool.nonblocking, func() error {
		return pool.Pool.Submit(task)
	})
}

func (pool *Pool) Release() {
	if pool == nil || pool.Pool == nil {
		return
	}
	pool.releaseMu.Lock()
	defer pool.releaseMu.Unlock()
	pool.Pool.Release()
}

func (pool *Pool) ReleaseTimeout(timeout time.Duration) error {
	if pool == nil || pool.Pool == nil || timeout <= 0 {
		return ErrInvalidPoolOptions
	}
	pool.releaseMu.Lock()
	defer pool.releaseMu.Unlock()
	return pool.Pool.ReleaseTimeout(timeout)
}

func (pool *Pool) ReleaseContext(ctx context.Context) error {
	if pool == nil || pool.Pool == nil || ctx == nil {
		return ErrInvalidPoolOptions
	}
	pool.releaseMu.Lock()
	defer pool.releaseMu.Unlock()
	return pool.Pool.ReleaseContext(ctx)
}

type PoolFunc struct {
	*ants.PoolWithFunc
	nonblocking bool
	releaseMu   sync.Mutex
}

func NewPoolFunc(
	size int,
	fn func(any),
	options ...Option,
) (*PoolFunc, error) {
	if size <= 0 || size > maxPoolSize {
		return nil, ErrInvalidPoolSize
	}
	if fn == nil {
		return nil, ErrLackPoolFunc
	}
	vendor, normalized, err := loadBoundedOptions(options...)
	if err != nil {
		return nil, err
	}
	pool, err := ants.NewPoolWithFunc(size, fn, vendor...)
	if err != nil {
		return nil, err
	}
	return &PoolFunc{
		PoolWithFunc: pool,
		nonblocking:  normalized.Nonblocking,
	}, nil
}

func (pool *PoolFunc) Invoke(value any) error {
	return pool.InvokeContext(context.Background(), value)
}

func (pool *PoolFunc) InvokeContext(ctx context.Context, value any) error {
	if pool == nil || pool.PoolWithFunc == nil {
		return ErrInvalidTask
	}
	return admit(ctx, pool.nonblocking, func() error {
		return pool.PoolWithFunc.Invoke(value)
	})
}

type GenericPool[T any] struct {
	*ants.PoolWithFuncGeneric[T]
	nonblocking bool
	releaseMu   sync.Mutex
}

func NewGenericPool[T any](
	size int,
	function func(T),
	options ...Option,
) (*GenericPool[T], error) {
	if size <= 0 || size > maxPoolSize {
		return nil, ErrInvalidPoolSize
	}
	if function == nil {
		return nil, ErrLackPoolFunc
	}
	vendor, normalized, err := loadBoundedOptions(options...)
	if err != nil {
		return nil, err
	}
	pool, err := ants.NewPoolWithFuncGeneric(size, function, vendor...)
	if err != nil {
		return nil, err
	}
	return &GenericPool[T]{
		PoolWithFuncGeneric: pool,
		nonblocking:         normalized.Nonblocking,
	}, nil
}

func (pool *GenericPool[T]) Invoke(value T) error {
	return pool.InvokeContext(context.Background(), value)
}

func (pool *GenericPool[T]) InvokeContext(
	ctx context.Context,
	value T,
) error {
	if pool == nil || pool.PoolWithFuncGeneric == nil {
		return ErrInvalidTask
	}
	return admit(ctx, pool.nonblocking, func() error {
		return pool.PoolWithFuncGeneric.Invoke(value)
	})
}

func (pool *GenericPool[T]) Release() {
	if pool == nil || pool.PoolWithFuncGeneric == nil {
		return
	}
	pool.releaseMu.Lock()
	defer pool.releaseMu.Unlock()
	pool.PoolWithFuncGeneric.Release()
}

func (pool *GenericPool[T]) ReleaseTimeout(timeout time.Duration) error {
	if pool == nil || pool.PoolWithFuncGeneric == nil || timeout <= 0 {
		return ErrInvalidPoolOptions
	}
	pool.releaseMu.Lock()
	defer pool.releaseMu.Unlock()
	return pool.PoolWithFuncGeneric.ReleaseTimeout(timeout)
}

func (pool *GenericPool[T]) ReleaseContext(ctx context.Context) error {
	if pool == nil || pool.PoolWithFuncGeneric == nil || ctx == nil {
		return ErrInvalidPoolOptions
	}
	pool.releaseMu.Lock()
	defer pool.releaseMu.Unlock()
	return pool.PoolWithFuncGeneric.ReleaseContext(ctx)
}

func admit(
	ctx context.Context,
	nonblocking bool,
	operation func() error,
) error {
	if ctx == nil || operation == nil {
		return ErrInvalidTask
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if !errors.Is(err, ErrPoolOverload) || nonblocking {
			return err
		}
		timer := time.NewTimer(admissionPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
