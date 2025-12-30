package workerpool

import "errors"

var (
	// ErrPoolClosed will be returned when submitting task to a closed pool.
	ErrPoolClosed = errors.New("this pool has been closed")

	// ErrPoolOverload will be returned when the pool is full and no workers available.
	ErrPoolOverload = errors.New("too many goroutines blocked on submit or nonblocking pool is full")

	// ErrLackPoolFunc will be returned when the generic pool function is nil.
	ErrLackPoolFunc = errors.New("must provide function for pool")

	// ErrInvalidMultiPoolSize will be returned when the size of multi-pool is invalid.
	ErrInvalidMultiPoolSize = errors.New("invalid multi-pool size")

	// ErrInvalidLoadBalancingStrategy will be returned when the load-balancing strategy is invalid.
	ErrInvalidLoadBalancingStrategy = errors.New("invalid load-balancing strategy")

	// ErrInvalidPoolIndex will be returned when the pool index is invalid.
	ErrInvalidPoolIndex = errors.New("invalid pool index")

	// ErrQueueIsFull will be returned when the worker queue is full.
	ErrQueueIsFull = errors.New("the queue is full")

	// ErrInvalidPreAllocSize will be returned when trying to set up a negative capacity under PreAlloc mode.
	ErrInvalidPreAllocSize = errors.New("can not set up a negative capacity under PreAlloc mode")

	// ErrTimeout will be returned after the operations timed out.
	ErrTimeout = errors.New("operation timed out")

	// ErrInvalidPoolSize will be returned when the pool size is <= 0.
	ErrInvalidPoolSize = errors.New("size must be greater than 0")
)
