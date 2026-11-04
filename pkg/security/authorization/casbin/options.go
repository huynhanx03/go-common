package casbin

import (
	"time"
)

type Limits struct {
	MaxModelBytes int
	MaxRules      int
	MaxValueBytes int
}

type runtimeOptions struct {
	clock  func() time.Time
	limits Limits
}

type Option func(*runtimeOptions) error

func WithClock(clock func() time.Time) Option {
	return func(options *runtimeOptions) error {
		if clock == nil {
			return ErrInvalidOption
		}
		options.clock = clock
		return nil
	}
}

func WithLimits(limits Limits) Option {
	return func(options *runtimeOptions) error {
		if limits.MaxModelBytes <= 0 ||
			limits.MaxModelBytes > maxModelBytes ||
			limits.MaxRules <= 0 ||
			limits.MaxRules > maxRules ||
			limits.MaxValueBytes <= 0 ||
			limits.MaxValueBytes > maxValueBytes {
			return ErrInvalidOption
		}
		options.limits = limits
		return nil
	}
}

func defaultRuntimeOptions() runtimeOptions {
	return runtimeOptions{
		clock: time.Now,
		limits: Limits{
			MaxModelBytes: defaultMaxModelBytes,
			MaxRules:      defaultMaxRules,
			MaxValueBytes: defaultMaxValueBytes,
		},
	}
}

func configuredRuntimeOptions(optionFunctions ...Option) (runtimeOptions, error) {
	options := defaultRuntimeOptions()
	for _, option := range optionFunctions {
		if option == nil {
			return runtimeOptions{}, ErrInvalidOption
		}
		if err := option(&options); err != nil {
			return runtimeOptions{}, err
		}
	}
	return options, nil
}
