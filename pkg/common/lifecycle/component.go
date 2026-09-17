// Package lifecycle coordinates independently running application components
// under one caller-owned context and one bounded shutdown sequence.
package lifecycle

import (
	"context"
	"time"
)

const (
	defaultShutdownTimeout = 30 * time.Second
	defaultMaxComponents   = 128
	maximumShutdownTimeout = 24 * time.Hour
	maximumComponents      = 4096
	maxComponentNameBytes  = 64
)

// Component is a long-running process component. Run must honor context
// cancellation. Shutdown must be idempotent and honor its deadline.
type Component interface {
	Name() string
	Run(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// ComponentOptions describes failure and shutdown semantics for a component.
type ComponentOptions struct {
	Required        bool
	ShutdownTimeout time.Duration
}

// Options configures the lifecycle runner.
type Options struct {
	// ShutdownTimeout is the total shutdown budget. Zero uses 30 seconds.
	ShutdownTimeout time.Duration
	// MaxComponents bounds registrations. Zero uses 128.
	MaxComponents int
}

func (options Options) withDefaults() Options {
	if options.ShutdownTimeout == 0 {
		options.ShutdownTimeout = defaultShutdownTimeout
	}
	if options.MaxComponents == 0 {
		options.MaxComponents = defaultMaxComponents
	}
	return options
}
