package workerpool

import "time"

// Option represents the optional function.
type Option func(opts *Options)

func loadOptions(options ...Option) *Options {
	opts := new(Options)
	for i := range options {
		options[i](opts)
	}
	return opts
}

// Options contains all options which will be applied when instantiating a pool.
type Options struct {
	// ExpiryDuration is the interval time to clean up expired workers.
	ExpiryDuration time.Duration

	// PreAlloc indicates whether to pre-allocate memory for workers/queue in the pool.
	PreAlloc bool

	// MaxBlockingTasks is the maximum number of goroutines that are blocked when it reaches the capacity of pool.
	MaxBlockingTasks int

	// Nonblocking indicates that pool will return nil/error when there is no available workers.
	Nonblocking bool

	// PanicHandler is the function to handle panics.
	PanicHandler func(any)

	// DisablePurge indicates whether to turn off the automatic purge of expired workers.
	DisablePurge bool
}

// WithExpiryDuration sets up the interval time of cleaning up goroutines.
func WithExpiryDuration(expiryDuration time.Duration) Option {
	return func(opts *Options) {
		opts.ExpiryDuration = expiryDuration
	}
}

// WithPreAlloc indicates whether it should malloc for workers.
func WithPreAlloc(preAlloc bool) Option {
	return func(opts *Options) {
		opts.PreAlloc = preAlloc
	}
}

// WithMaxBlockingTasks sets up the maximum number of goroutines that are blocked when it reaches the capacity of pool.
func WithMaxBlockingTasks(maxBlockingTasks int) Option {
	return func(opts *Options) {
		opts.MaxBlockingTasks = maxBlockingTasks
	}
}

// WithNonblocking indicates that pool will return nil when there is no available workers.
func WithNonblocking(nonblocking bool) Option {
	return func(opts *Options) {
		opts.Nonblocking = nonblocking
	}
}

// WithPanicHandler sets up panic handler.
func WithPanicHandler(panicHandler func(any)) Option {
	return func(opts *Options) {
		opts.PanicHandler = panicHandler
	}
}

// WithDisablePurge indicates whether we turn off automatically purge.
func WithDisablePurge(disable bool) Option {
	return func(opts *Options) {
		opts.DisablePurge = disable
	}
}
