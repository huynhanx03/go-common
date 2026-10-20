package casbin

import (
	"time"
)

const (
	// DefaultMaxRoleHierarchyDepth is Casbin's maximum number of relationship
	// edges followed by the built-in role manager. Exporting the contract lets
	// applications validate projections without depending on Casbin internals.
	DefaultMaxRoleHierarchyDepth = 10
	defaultMaxModelBytes         = 128 << 10
	hardMaxModelBytes            = 1 << 20
	defaultMaxRules              = 100_000
	hardMaxRules                 = 1_000_000
	defaultMaxValueBytes         = 512
	hardMaxValueBytes            = 4096
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
			limits.MaxModelBytes > hardMaxModelBytes ||
			limits.MaxRules <= 0 ||
			limits.MaxRules > hardMaxRules ||
			limits.MaxValueBytes <= 0 ||
			limits.MaxValueBytes > hardMaxValueBytes {
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
