package workerpool

import (
	"math"
)

// GenericMultiPool consists of multiple GenericPools.
type GenericMultiPool[T any] struct {
	*multiPoolCommon[*GenericPool[T]]
}

// NewGenericMultiPool instantiates a GenericMultiPool.
func NewGenericMultiPool[T any](size, sizePerPool int, fn func(T), lbs LoadBalancingStrategy, options ...Option) (*GenericMultiPool[T], error) {
	if size <= 0 {
		return nil, ErrInvalidPoolSize
	}

	if lbs != RoundRobin && lbs != LeastTasks {
		return nil, ErrInvalidLoadBalancingStrategy
	}

	pools := make([]*GenericPool[T], size)
	for i := 0; i < size; i++ {
		pool, err := NewGenericPool(sizePerPool, fn, options...)
		if err != nil {
			return nil, err
		}
		pools[i] = pool
	}
	return &GenericMultiPool[T]{
		multiPoolCommon: &multiPoolCommon[*GenericPool[T]]{
			pools: pools,
			index: math.MaxUint32,
			lbs:   lbs,
		},
	}, nil
}

func (mp *GenericMultiPool[T]) Invoke(arg T) (err error) {
	if mp.IsClosed() {
		return ErrPoolClosed
	}
	if err = mp.pools[mp.next(mp.lbs)].Invoke(arg); err == nil {
		return
	}
	if err == ErrPoolOverload && mp.lbs == RoundRobin {
		return mp.pools[mp.next(LeastTasks)].Invoke(arg)
	}
	return
}
