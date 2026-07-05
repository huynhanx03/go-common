package workerpool

import (
	"github.com/panjf2000/ants/v2"
)

// Errors re-exported so callers can match with errors.Is against the
// workerpool package without importing ants.
var (
	// ErrLackPoolFunc will be returned when invokers don't provide function for pool.
	ErrLackPoolFunc = ants.ErrLackPoolFunc

	// ErrInvalidPoolExpiry will be returned when setting a negative number as the periodic duration to purge goroutines.
	ErrInvalidPoolExpiry = ants.ErrInvalidPoolExpiry

	// ErrPoolClosed will be returned when submitting task to a closed pool.
	ErrPoolClosed = ants.ErrPoolClosed

	// ErrPoolOverload will be returned when the pool is full and no workers available.
	ErrPoolOverload = ants.ErrPoolOverload

	// ErrInvalidPreAllocSize will be returned when trying to set up a negative capacity under PreAlloc mode.
	ErrInvalidPreAllocSize = ants.ErrInvalidPreAllocSize

	// ErrTimeout will be returned after the operations timed out.
	ErrTimeout = ants.ErrTimeout

	// ErrInvalidPoolIndex will be returned when trying to retrieve a pool with an invalid index from a multi-pool.
	ErrInvalidPoolIndex = ants.ErrInvalidPoolIndex

	// ErrInvalidLoadBalancingStrategy will be returned when creating a multi-pool with an unknown strategy.
	ErrInvalidLoadBalancingStrategy = ants.ErrInvalidLoadBalancingStrategy

	// ErrInvalidMultiPoolSize will be returned when creating a multi-pool with a non-positive size.
	ErrInvalidMultiPoolSize = ants.ErrInvalidMultiPoolSize
)
