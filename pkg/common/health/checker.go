// Package health provides bounded liveness and readiness checks whose public
// results never expose raw dependency errors.
package health

import (
	"context"
	"time"
)

// Checker reports whether one dependency or event loop is healthy.
type Checker interface {
	Check(ctx context.Context) error
}

// Probe is a bitmask selecting the endpoints on which a check runs.
type Probe uint8

const (
	ProbeLiveness Probe = 1 << iota
	ProbeReadiness
)

// CheckOptions configures one registered checker.
type CheckOptions struct {
	Probes    Probe
	Critical  bool
	Timeout   time.Duration
	ErrorCode string
}

// Status is the safe public result of one health check.
type Status struct {
	Name      string        `json:"name"`
	Healthy   bool          `json:"healthy"`
	Critical  bool          `json:"critical"`
	Duration  time.Duration `json:"duration"`
	ErrorCode string        `json:"error_code,omitempty"`
}

// Report is the stable response returned by health transports.
type Report struct {
	Healthy  bool     `json:"healthy"`
	Statuses []Status `json:"statuses"`
}

// Options configures registry bounds and clock measurement.
type Options struct {
	MaxConcurrent int
	MaxChecks     int
	Now           func() time.Time
}
