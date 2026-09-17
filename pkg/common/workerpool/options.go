package workerpool

import (
	"fmt"
	"runtime/debug"
	"time"

	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

// Logger is the minimal logging interface the pools write to.
type Logger = ants.Logger

// Option represents the optional function.
type Option func(opts *Options)

func loadOptions(options ...Option) []ants.Option {
	opts, _ := normalizeOptions(options...)
	return vendorOptions(opts, opts.Nonblocking)
}

func loadBoundedOptions(options ...Option) ([]ants.Option, Options, error) {
	opts, err := normalizeOptions(options...)
	if err != nil {
		return nil, Options{}, err
	}
	return vendorOptions(opts, true), opts, nil
}

func normalizeOptions(options ...Option) (Options, error) {
	opts := Options{}
	for i := range options {
		if options[i] == nil {
			return Options{}, ErrInvalidPoolOptions
		}
		options[i](&opts)
	}
	if opts.ExpiryDuration < 0 || opts.MaxBlockingTasks < 0 {
		return Options{}, ErrInvalidPoolOptions
	}
	callerPanicHandler := opts.PanicHandler
	opts.PanicHandler = func(recovered any) {
		if callerPanicHandler != nil {
			func() {
				defer func() { _ = recover() }()
				callerPanicHandler(recovered)
			}()
			return
		}
		stack := debug.Stack()
		if len(stack) > 32<<10 {
			stack = stack[:32<<10]
		}
		zap.L().Error(
			"workerpool task panic recovered",
			zap.String("panic_type", fmt.Sprintf("%T", recovered)),
			zap.ByteString("stack", stack),
		)
	}
	return opts, nil
}

func vendorOptions(opts Options, nonblocking bool) []ants.Option {
	return []ants.Option{ants.WithOptions(ants.Options{
		ExpiryDuration:   opts.ExpiryDuration,
		PreAlloc:         opts.PreAlloc,
		MaxBlockingTasks: opts.MaxBlockingTasks,
		Nonblocking:      nonblocking,
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
	if l == nil {
		l = zap.L()
	}
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
