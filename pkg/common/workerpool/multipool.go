package workerpool

import (
	"github.com/panjf2000/ants/v2"
)

// LoadBalancingStrategy picks which sub-pool of a multi-pool receives the next task.
type LoadBalancingStrategy = ants.LoadBalancingStrategy

const (
	// RoundRobin distributes tasks to sub-pools in rotation.
	RoundRobin = ants.RoundRobin

	// LeastTasks always picks the sub-pool with the fewest pending tasks.
	LeastTasks = ants.LeastTasks
)

// MultiPool consists of multiple sub-pools, reducing contention on a single
// pool's lock for high-throughput workloads.
type MultiPool struct {
	*ants.MultiPool
}

// NewMultiPool creates a multi-pool with size sub-pools of sizePerPool workers each.
func NewMultiPool(size, sizePerPool int, lbs LoadBalancingStrategy, options ...Option) (*MultiPool, error) {
	p, err := ants.NewMultiPool(size, sizePerPool, lbs, loadOptions(options...)...)
	if err != nil {
		return nil, err
	}
	return &MultiPool{MultiPool: p}, nil
}

// MultiPoolFunc is a MultiPool bound to a fixed function taking an untyped
// argument. Prefer GenericMultiPool when the argument type is known.
type MultiPoolFunc struct {
	*ants.MultiPoolWithFunc
}

// NewMultiPoolFunc creates a multi-pool bound to fn.
func NewMultiPoolFunc(size, sizePerPool int, fn func(any), lbs LoadBalancingStrategy, options ...Option) (*MultiPoolFunc, error) {
	p, err := ants.NewMultiPoolWithFunc(size, sizePerPool, fn, lbs, loadOptions(options...)...)
	if err != nil {
		return nil, err
	}
	return &MultiPoolFunc{MultiPoolWithFunc: p}, nil
}

// GenericMultiPool is a MultiPool bound to a typed function.
type GenericMultiPool[T any] struct {
	*ants.MultiPoolWithFuncGeneric[T]
}

// NewGenericMultiPool creates a multi-pool bound to a typed function.
func NewGenericMultiPool[T any](size, sizePerPool int, fn func(T), lbs LoadBalancingStrategy, options ...Option) (*GenericMultiPool[T], error) {
	p, err := ants.NewMultiPoolWithFuncGeneric(size, sizePerPool, fn, lbs, loadOptions(options...)...)
	if err != nil {
		return nil, err
	}
	return &GenericMultiPool[T]{MultiPoolWithFuncGeneric: p}, nil
}
