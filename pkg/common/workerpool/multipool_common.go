package workerpool

import (
	"math"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// LoadBalancingStrategy represents the type of load-balancing algorithm.
type LoadBalancingStrategy int

const (
	// RoundRobin distributes task to a list of pools in rotation.
	RoundRobin LoadBalancingStrategy = 1 << iota

	// LeastTasks always selects the pool with the least number of pending tasks.
	LeastTasks
)

// Pooler defines the common interface for a worker pool (generic or not).
type Pooler interface {
	Running() int
	Free() int
	Waiting() int
	Cap() int
	Tune(size int)
	IsClosed() bool
	Release()
	ReleaseTimeout(timeout time.Duration) error
	Reboot()
}

// multiPoolCommon contains the common logic for MultiPool and GenericMultiPool.
type multiPoolCommon[P Pooler] struct {
	pools []P
	index uint32
	state int32
	lbs   LoadBalancingStrategy
}

// next returns the index of the next pool to be used based on the load-balancing strategy.
func (mp *multiPoolCommon[P]) next(lbs LoadBalancingStrategy) (idx int) {
	switch lbs {
	case RoundRobin:
		return int(atomic.AddUint32(&mp.index, 1) % uint32(len(mp.pools)))
	case LeastTasks:
		leastTasks := math.MaxInt
		for i := range mp.pools {
			if n := mp.pools[i].Running(); n < leastTasks {
				leastTasks = n
				idx = i
			}
		}
		return
	}
	return -1
}

// Running returns the total number of running tasks in all pools.
func (mp *multiPoolCommon[P]) Running() (n int) {
	for i := range mp.pools {
		n += mp.pools[i].Running()
	}
	return
}

// Free returns the total number of free tasks in all pools.
func (mp *multiPoolCommon[P]) Free() (n int) {
	for i := range mp.pools {
		n += mp.pools[i].Free()
	}
	return
}

// Waiting returns the total number of waiting tasks in all pools.
func (mp *multiPoolCommon[P]) Waiting() (n int) {
	for i := range mp.pools {
		n += mp.pools[i].Waiting()
	}
	return
}

// Cap returns the total capacity of all pools.
func (mp *multiPoolCommon[P]) Cap() (n int) {
	for i := range mp.pools {
		n += mp.pools[i].Cap()
	}
	return
}

// Tune tunes the capacity of all pools.
func (mp *multiPoolCommon[P]) Tune(size int) {
	for i := range mp.pools {
		mp.pools[i].Tune(size)
	}
}

// IsClosed returns true if all pools are closed.
func (mp *multiPoolCommon[P]) IsClosed() bool {
	return atomic.LoadInt32(&mp.state) == CLOSED
}

// Release releases all pools.
func (mp *multiPoolCommon[P]) Release() {
	if !atomic.CompareAndSwapInt32(&mp.state, OPENED, CLOSED) {
		return
	}

	for i := range mp.pools {
		mp.pools[i].Release()
	}
}

// ReleaseTimeout closes the multi-pool with a timeout,
// it waits all pools to be closed before timing out.
func (mp *multiPoolCommon[P]) ReleaseTimeout(timeout time.Duration) error {
	if !atomic.CompareAndSwapInt32(&mp.state, OPENED, CLOSED) {
		return ErrPoolClosed
	}

	var wg errgroup.Group
	for i := range mp.pools {
		func(p P) {
			wg.Go(func() error {
				return p.ReleaseTimeout(timeout)
			})
		}(mp.pools[i])
	}

	return wg.Wait()
}

// Reboot reboots all pools.
func (mp *multiPoolCommon[P]) Reboot() {
	if atomic.CompareAndSwapInt32(&mp.state, CLOSED, OPENED) {
		atomic.StoreUint32(&mp.index, 0)
		for i := range mp.pools {
			mp.pools[i].Reboot()
		}
	}
}
