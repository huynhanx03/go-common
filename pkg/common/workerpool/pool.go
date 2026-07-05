// Package workerpool provides goroutine pools backed by ants
// (github.com/panjf2000/ants). It exposes the pool types behind
// package-local names so applications never import ants directly.
package workerpool

import (
	"github.com/panjf2000/ants/v2"
)

// Pool accepts tasks as closures and processes them via a pool of workers.
type Pool struct {
	*ants.Pool
}

// NewPool creates a new pool. A non-positive size means an unbounded pool.
func NewPool(size int, options ...Option) (*Pool, error) {
	p, err := ants.NewPool(size, loadOptions(options...)...)
	if err != nil {
		return nil, err
	}
	return &Pool{Pool: p}, nil
}

// PoolFunc processes tasks with a fixed function taking an untyped argument.
// Prefer GenericPool when the argument type is known at compile time.
type PoolFunc struct {
	*ants.PoolWithFunc
}

// NewPoolFunc creates a new pool bound to fn.
func NewPoolFunc(size int, fn func(any), options ...Option) (*PoolFunc, error) {
	p, err := ants.NewPoolWithFunc(size, fn, loadOptions(options...)...)
	if err != nil {
		return nil, err
	}
	return &PoolFunc{PoolWithFunc: p}, nil
}

// GenericPool accepts typed tasks and processes them via a pool of workers.
type GenericPool[T any] struct {
	*ants.PoolWithFuncGeneric[T]
}

// NewGenericPool creates a new pool bound to a typed function.
func NewGenericPool[T any](size int, pf func(T), options ...Option) (*GenericPool[T], error) {
	p, err := ants.NewPoolWithFuncGeneric(size, pf, loadOptions(options...)...)
	if err != nil {
		return nil, err
	}
	return &GenericPool[T]{PoolWithFuncGeneric: p}, nil
}
