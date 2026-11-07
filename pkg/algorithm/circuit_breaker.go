package algorithm

import (
	"errors"
	"sync"
	"time"
)

// Circuit breaker states.
const (
	StateClosed   = "closed"
	StateOpen     = "open"
	StateHalfOpen = "half_open"
)

// Circuit breaker defaults.
const (
	defaultFailureThreshold = 5
	defaultSuccessThreshold = 2
	defaultOpenTimeout      = 30 * time.Second
)

// Sentinel errors.
var (
	ErrCircuitOpen      = errors.New("circuit breaker is open")
	ErrInvalidOperation = errors.New("circuit breaker operation is nil")
)

// CircuitBreaker implements the circuit breaker pattern.
// States: Closed → Open → HalfOpen → Closed (or back to Open).
type CircuitBreaker struct {
	mu               sync.Mutex
	state            string
	failureCount     int
	successCount     int
	failureThreshold int
	successThreshold int
	openTimeout      time.Duration
	lastFailure      int64
	generation       uint64
	probeInFlight    bool
	onStateChange    func(from, to string)
	now              func() int64
}

type circuitPermit struct {
	generation uint64
	state      string
}

// CircuitBreakerOption configures a CircuitBreaker.
type CircuitBreakerOption func(*CircuitBreaker)

// WithFailureThreshold sets consecutive failures needed to open the circuit.
func WithFailureThreshold(n int) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		if n > 0 {
			cb.failureThreshold = n
		}
	}
}

// WithSuccessThreshold sets consecutive successes in half-open to close the circuit.
func WithSuccessThreshold(n int) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		if n > 0 {
			cb.successThreshold = n
		}
	}
}

// WithOpenTimeout sets how long the circuit stays open before transitioning to half-open.
func WithOpenTimeout(d time.Duration) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		if d > 0 {
			cb.openTimeout = d
		}
	}
}

// WithOnStateChange sets a callback invoked on state transitions.
func WithOnStateChange(fn func(from, to string)) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		cb.onStateChange = fn
	}
}

// WithBreakerClock overrides the time source (unix nanoseconds).
func WithBreakerClock(fn func() int64) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		if fn != nil {
			cb.now = fn
		}
	}
}

// NewCircuitBreaker creates a circuit breaker with the given options.
func NewCircuitBreaker(opts ...CircuitBreakerOption) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: defaultFailureThreshold,
		successThreshold: defaultSuccessThreshold,
		openTimeout:      defaultOpenTimeout,
		now:              func() int64 { return time.Now().UnixNano() },
	}
	for _, opt := range opts {
		opt(cb)
	}
	return cb
}

// Allow checks if a request is permitted through the circuit breaker.
// Returns ErrCircuitOpen if the circuit is open.
//
// Deprecated: use Execute to associate admission with its result and limit
// concurrent half-open probes.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	var notify func()
	defer func() {
		cb.mu.Unlock()
		if notify != nil {
			notify()
		}
	}()

	switch cb.state {
	case StateClosed:
		return nil
	case StateOpen:
		if cb.now()-cb.lastFailure >= int64(cb.openTimeout) {
			notify = cb.transition(StateHalfOpen)
			return nil
		}
		return ErrCircuitOpen
	case StateHalfOpen:
		return nil
	default:
		return nil
	}
}

// Execute runs operation when the circuit admits a call. The success result
// controls breaker accounting independently from the operation error returned
// to the caller. Only one operation may run as a half-open probe at a time.
// Return false for an outcome that should count as a dependency failure, even
// when the operation itself returned no error.
func (cb *CircuitBreaker) Execute(operation func() (success bool, err error)) error {
	if operation == nil {
		return ErrInvalidOperation
	}

	permit, notify, err := cb.reserve()
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			cb.release(permit)
		}
	}()

	if notify != nil {
		notify()
	}
	success, operationErr := operation()
	completed = true
	cb.complete(permit, success)
	return operationErr
}

func (cb *CircuitBreaker) reserve() (circuitPermit, func(), error) {
	cb.mu.Lock()
	var notify func()

	if cb.state == StateOpen {
		if cb.now()-cb.lastFailure < int64(cb.openTimeout) {
			cb.mu.Unlock()
			return circuitPermit{}, nil, ErrCircuitOpen
		}
		notify = cb.transition(StateHalfOpen)
	}

	if cb.state == StateHalfOpen {
		if cb.probeInFlight {
			cb.mu.Unlock()
			if notify != nil {
				notify()
			}
			return circuitPermit{}, nil, ErrCircuitOpen
		}
		cb.probeInFlight = true
	}

	permit := circuitPermit{generation: cb.generation, state: cb.state}
	cb.mu.Unlock()
	return permit, notify, nil
}

func (cb *CircuitBreaker) complete(permit circuitPermit, success bool) {
	cb.mu.Lock()
	if permit.generation != cb.generation || permit.state != cb.state {
		cb.mu.Unlock()
		return
	}

	var notify func()
	switch permit.state {
	case StateClosed:
		if success {
			cb.failureCount = 0
		} else {
			cb.lastFailure = cb.now()
			cb.failureCount++
			if cb.failureCount >= cb.failureThreshold {
				notify = cb.transition(StateOpen)
			}
		}
	case StateHalfOpen:
		cb.probeInFlight = false
		if success {
			cb.successCount++
			if cb.successCount >= cb.successThreshold {
				notify = cb.transition(StateClosed)
			}
		} else {
			cb.lastFailure = cb.now()
			notify = cb.transition(StateOpen)
		}
	}
	cb.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func (cb *CircuitBreaker) release(permit circuitPermit) {
	if permit.state != StateHalfOpen {
		return
	}
	cb.mu.Lock()
	if permit.generation == cb.generation && cb.state == StateHalfOpen {
		cb.probeInFlight = false
	}
	cb.mu.Unlock()
}

// RecordSuccess records a successful operation.
//
// Deprecated: use Execute to associate admission with its result and limit
// concurrent half-open probes.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	var notify func()
	defer func() {
		cb.mu.Unlock()
		if notify != nil {
			notify()
		}
	}()

	switch cb.state {
	case StateClosed:
		cb.failureCount = 0
	case StateHalfOpen:
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			notify = cb.transition(StateClosed)
		}
	}
}

// RecordFailure records a failed operation.
//
// Deprecated: use Execute to associate admission with its result and limit
// concurrent half-open probes.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	var notify func()
	defer func() {
		cb.mu.Unlock()
		if notify != nil {
			notify()
		}
	}()

	cb.lastFailure = cb.now()

	switch cb.state {
	case StateClosed:
		cb.failureCount++
		if cb.failureCount >= cb.failureThreshold {
			notify = cb.transition(StateOpen)
		}
	case StateHalfOpen:
		notify = cb.transition(StateOpen)
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset returns the circuit breaker to the closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	notify := cb.transition(StateClosed)
	cb.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// Counts returns current failure and success counters.
func (cb *CircuitBreaker) Counts() (failures, successes int) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failureCount, cb.successCount
}

// transition moves to a new state and returns a callback to fire AFTER releasing the lock.
// Caller must hold cb.mu.
func (cb *CircuitBreaker) transition(to string) func() {
	from := cb.state
	cb.state = to
	cb.generation++
	cb.failureCount = 0
	cb.successCount = 0
	cb.probeInFlight = false

	if cb.onStateChange != nil && from != to {
		fn := cb.onStateChange
		return func() { fn(from, to) }
	}
	return nil
}
