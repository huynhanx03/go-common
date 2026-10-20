package health

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	maxGateReasonBytes  = 1024
	maximumHeartbeatAge = 365 * 24 * time.Hour
)

var (
	ErrGateClosed     = errors.New("health: gate is closed")
	ErrHeartbeatStale = errors.New("health: heartbeat is stale")
)

// Gate is a concurrency-safe, fail-closed health checker for lifecycle state.
//
// A zero Gate is closed. Processes normally open a readiness gate only after
// every admission dependency is initialized, then close it before draining.
// The optional reason is private process state and is never returned by Check,
// so transports cannot accidentally disclose operational details.
type Gate struct {
	mu     sync.RWMutex
	open   bool
	reason string
}

// Open marks the gate healthy.
func (gate *Gate) Open() {
	if gate == nil {
		return
	}
	gate.mu.Lock()
	gate.open = true
	gate.reason = ""
	gate.mu.Unlock()
}

// Close marks the gate unhealthy. reason is retained only for trusted local
// diagnostics; it is intentionally absent from the Checker result.
func (gate *Gate) Close(reason string) {
	if gate == nil {
		return
	}
	gate.mu.Lock()
	gate.open = false
	gate.reason = boundedReason(reason)
	gate.mu.Unlock()
}

// IsOpen reports the current state without changing it.
func (gate *Gate) IsOpen() bool {
	if gate == nil {
		return false
	}
	gate.mu.RLock()
	open := gate.open
	gate.mu.RUnlock()
	return open
}

// Reason returns the private diagnostic reason. Never expose it directly on a
// public health endpoint because callers may place dependency errors in it.
func (gate *Gate) Reason() string {
	if gate == nil {
		return ""
	}
	gate.mu.RLock()
	reason := gate.reason
	gate.mu.RUnlock()
	return reason
}

// Check implements Checker.
func (gate *Gate) Check(ctx context.Context) error {
	if ctx == nil {
		return ErrGateClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !gate.IsOpen() {
		return ErrGateClosed
	}
	return nil
}

// Heartbeat is a bounded freshness checker for externally owned event loops.
// It owns no goroutine. The event loop calls Beat after successful work or an
// explicit successful idle poll.
type Heartbeat struct {
	mu       sync.RWMutex
	lastBeat time.Time
	maxAge   time.Duration
	now      func() time.Time
}

// NewHeartbeat constructs a fail-closed heartbeat. It remains unhealthy until
// the first Beat call.
func NewHeartbeat(maxAge time.Duration, now func() time.Time) (*Heartbeat, error) {
	if maxAge <= 0 || maxAge > maximumHeartbeatAge {
		return nil, ErrInvalidOptions
	}
	if now == nil {
		now = time.Now
	}
	return &Heartbeat{maxAge: maxAge, now: now}, nil
}

func boundedReason(reason string) string {
	if len(reason) > maxGateReasonBytes {
		reason = reason[:maxGateReasonBytes]
	}
	return strings.Clone(strings.ToValidUTF8(reason, ""))
}

// Beat records one successful event-loop observation.
func (heartbeat *Heartbeat) Beat() {
	if heartbeat == nil || heartbeat.now == nil {
		return
	}
	observed := heartbeat.now().UTC()
	heartbeat.mu.Lock()
	if heartbeat.lastBeat.IsZero() || !observed.Before(heartbeat.lastBeat) {
		heartbeat.lastBeat = observed
	}
	heartbeat.mu.Unlock()
}

// LastBeat returns a snapshot suitable for trusted metrics. A zero value means
// no success has been observed.
func (heartbeat *Heartbeat) LastBeat() time.Time {
	if heartbeat == nil {
		return time.Time{}
	}
	heartbeat.mu.RLock()
	observed := heartbeat.lastBeat
	heartbeat.mu.RUnlock()
	return observed
}

// Check implements Checker.
func (heartbeat *Heartbeat) Check(ctx context.Context) error {
	if heartbeat == nil || heartbeat.now == nil || heartbeat.maxAge <= 0 || ctx == nil {
		return ErrHeartbeatStale
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	observed := heartbeat.LastBeat()
	now := heartbeat.now().UTC()
	if observed.IsZero() || observed.After(now) || now.Sub(observed) > heartbeat.maxAge {
		return ErrHeartbeatStale
	}
	return nil
}

// CheckerFunc adapts a function into a Checker without introducing a wrapper
// type in every service.
type CheckerFunc func(context.Context) error

// Check implements Checker and fails closed for a nil function.
func (check CheckerFunc) Check(ctx context.Context) error {
	if check == nil {
		return ErrInvalidCheck
	}
	return check(ctx)
}

var (
	_ Checker = (*Gate)(nil)
	_ Checker = (*Heartbeat)(nil)
	_ Checker = CheckerFunc(nil)
)
