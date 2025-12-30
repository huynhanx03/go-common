package workerpool

import "math"

// MultiPool consists of multiple pools, from which you will benefit the
// performance improvement on basis of the fine-grained locking that reduces
// the lock contention.
type MultiPool struct {
	*multiPoolCommon[*Pool]
}

// NewMultiPool instantiates a MultiPool with a size of the pool list and a size
// per pool, and the load-balancing strategy.
func NewMultiPool(size, sizePerPool int, lbs LoadBalancingStrategy, options ...Option) (*MultiPool, error) {
	if size <= 0 {
		return nil, ErrInvalidPoolSize
	}

	if lbs != RoundRobin && lbs != LeastTasks {
		return nil, ErrInvalidLoadBalancingStrategy
	}

	pools := make([]*Pool, size)
	for i := 0; i < size; i++ {
		pool, err := NewPool(sizePerPool, options...)
		if err != nil {
			return nil, err
		}
		pools[i] = pool
	}
	return &MultiPool{
		multiPoolCommon: &multiPoolCommon[*Pool]{
			pools: pools,
			index: math.MaxUint32,
			lbs:   lbs,
		},
	}, nil
}

func (mp *MultiPool) Submit(task func()) (err error) {
	if mp.IsClosed() {
		return ErrPoolClosed
	}
	if err = mp.pools[mp.next(mp.lbs)].Submit(task); err == nil {
		return
	}
	if err == ErrPoolOverload && mp.lbs == RoundRobin {
		return mp.pools[mp.next(LeastTasks)].Submit(task)
	}
	return
}
