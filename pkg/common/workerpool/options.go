package workerpool

import (
	"time"

	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

// Logger is the minimal logging interface the pools write to.
type Logger = ants.Logger

// Option represents the optional function.
type Option func(opts *Options)

func loadOptions(options ...Option) []ants.Option {
	opts := new(Options)
	for i := range options {
		options[i](opts)
	}
	return []ants.Option{ants.WithOptions(ants.Options{
		ExpiryDuration:   opts.ExpiryDuration,
		PreAlloc:         opts.PreAlloc,
		MaxBlockingTasks: opts.MaxBlockingTasks,
		Nonblocking:      opts.Nonblocking,
		PanicHandler:     opts.PanicHandler,
		Logger:           opts.Logger,
		DisablePurge:     opts.DisablePurge,
	})}
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

	// Logger is the customized logger for logging info; the pool default is used when nil.
	Logger Logger

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

// WithLogger sets up a customized logger.
func WithLogger(logger Logger) Option {
	return func(opts *Options) {
		opts.Logger = logger
	}
}

// WithZapLogger routes pool logs to a zap logger.
func WithZapLogger(l *zap.Logger) Option {
	return WithLogger(zapLogger{sugar: l.Sugar()})
}

// WithDisablePurge indicates whether we turn off automatically purge.
func WithDisablePurge(disable bool) Option {
	return func(opts *Options) {
		opts.DisablePurge = disable
	}
}

type zapLogger struct {
	sugar *zap.SugaredLogger
}

func (z zapLogger) Printf(format string, args ...any) {
	z.sugar.Infof(format, args...)
}
