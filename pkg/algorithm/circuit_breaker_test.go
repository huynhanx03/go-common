package algorithm

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecuteLimitsHalfOpenToOneProbeAndClosesAfterSuccesses(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Unix(0, 0).UnixNano())
	breaker := NewCircuitBreaker(
		WithFailureThreshold(1),
		WithSuccessThreshold(2),
		WithOpenTimeout(time.Second),
		WithBreakerClock(now.Load),
	)

	dependencyErr := errors.New("dependency unavailable")
	if err := breaker.Execute(func() (bool, error) { return false, dependencyErr }); !errors.Is(err, dependencyErr) {
		t.Fatalf("first Execute() error = %v, want dependency error", err)
	}
	if got := breaker.State(); got != StateOpen {
		t.Fatalf("state after dependency failure = %q, want %q", got, StateOpen)
	}

	now.Add(int64(time.Second))
	probeStarted := make(chan struct{})
	finishProbe := make(chan struct{})
	probeResult := make(chan error, 1)
	go func() {
		probeResult <- breaker.Execute(func() (bool, error) {
			close(probeStarted)
			<-finishProbe
			return true, nil
		})
	}()
	<-probeStarted

	secondProbeCalled := false
	err := breaker.Execute(func() (bool, error) {
		secondProbeCalled = true
		return true, nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("concurrent Execute() error = %v, want ErrCircuitOpen", err)
	}
	if secondProbeCalled {
		t.Fatal("concurrent probe reached the dependency while the first probe was running")
	}

	close(finishProbe)
	if err := <-probeResult; err != nil {
		t.Fatalf("first probe Execute() error = %v", err)
	}
	if got := breaker.State(); got != StateHalfOpen {
		t.Fatalf("state after first successful probe = %q, want %q", got, StateHalfOpen)
	}

	if err := breaker.Execute(func() (bool, error) { return true, nil }); err != nil {
		t.Fatalf("second probe Execute() error = %v", err)
	}
	if got := breaker.State(); got != StateClosed {
		t.Fatalf("state after second successful probe = %q, want %q", got, StateClosed)
	}
}

func TestExecuteReopensAfterFailedHalfOpenProbe(t *testing.T) {
	var now atomic.Int64
	breaker := NewCircuitBreaker(
		WithFailureThreshold(1),
		WithOpenTimeout(time.Second),
		WithBreakerClock(now.Load),
	)

	dependencyErr := errors.New("dependency unavailable")
	_ = breaker.Execute(func() (bool, error) { return false, dependencyErr })
	now.Add(int64(time.Second))

	if err := breaker.Execute(func() (bool, error) { return false, dependencyErr }); !errors.Is(err, dependencyErr) {
		t.Fatalf("probe Execute() error = %v, want dependency error", err)
	}
	if got := breaker.State(); got != StateOpen {
		t.Fatalf("state after failed probe = %q, want %q", got, StateOpen)
	}

	called := false
	err := breaker.Execute(func() (bool, error) {
		called = true
		return true, nil
	})
	if !errors.Is(err, ErrCircuitOpen) || called {
		t.Fatalf("Execute() while reopened = %v, called = %t; want ErrCircuitOpen without calling dependency", err, called)
	}
}

func TestExecuteReleasesProbeAfterPanic(t *testing.T) {
	var now atomic.Int64
	breaker := NewCircuitBreaker(
		WithFailureThreshold(1),
		WithOpenTimeout(time.Second),
		WithBreakerClock(now.Load),
	)

	_ = breaker.Execute(func() (bool, error) { return false, errors.New("failure") })
	now.Add(int64(time.Second))

	func() {
		defer func() {
			if recovered := recover(); recovered != "probe panic" {
				t.Fatalf("panic = %v, want %q", recovered, "probe panic")
			}
		}()
		_ = breaker.Execute(func() (bool, error) { panic("probe panic") })
	}()

	if err := breaker.Execute(func() (bool, error) { return true, nil }); err != nil {
		t.Fatalf("Execute() after panicking probe = %v, want probe admission", err)
	}
}

func TestExecuteRejectsNilOperation(t *testing.T) {
	breaker := NewCircuitBreaker()
	if err := breaker.Execute(nil); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("Execute(nil) error = %v, want ErrInvalidOperation", err)
	}
}

func TestCircuitBreakerCallbacksCanInspectState(t *testing.T) {
	var breaker *CircuitBreaker
	callbackDone := make(chan struct{})
	breaker = NewCircuitBreaker(
		WithFailureThreshold(1),
		WithOnStateChange(func(_, to string) {
			if breaker.State() != to {
				return
			}
			breaker.Counts()
			close(callbackDone)
		}),
	)

	callDone := make(chan struct{})
	go func() {
		breaker.RecordFailure()
		close(callDone)
	}()

	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("state callback could not inspect the breaker")
	}
	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("failure recording did not return")
	}
}

func TestCircuitBreakerSuccessClearsConsecutiveFailures(t *testing.T) {
	breaker := NewCircuitBreaker(WithFailureThreshold(3))
	breaker.RecordFailure()
	breaker.RecordFailure()
	breaker.RecordSuccess()
	if failures, _ := breaker.Counts(); failures != 0 {
		t.Fatalf("failures after success = %d, want 0", failures)
	}
	breaker.RecordFailure()
	breaker.RecordFailure()
	if got := breaker.State(); got != StateClosed {
		t.Fatalf("state after two post-success failures = %q, want %q", got, StateClosed)
	}
}

func TestAllowAdmitsOnlyAfterOpenTimeoutAndClosesHalfOpenCircuit(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Unix(0, 0).UnixNano())
	breaker := NewCircuitBreaker(
		WithFailureThreshold(2),
		WithSuccessThreshold(2),
		WithOpenTimeout(time.Second),
		WithBreakerClock(now.Load),
	)

	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow while closed = %v", err)
	}
	breaker.RecordFailure()
	breaker.RecordFailure()
	if err := breaker.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Allow before timeout = %v; want circuit open", err)
	}

	now.Add(int64(time.Second))
	if err := breaker.Allow(); err != nil || breaker.State() != StateHalfOpen {
		t.Fatalf("Allow at timeout = %v, state = %q; want half-open admission", err, breaker.State())
	}
	breaker.RecordSuccess()
	if breaker.State() != StateHalfOpen {
		t.Fatal("one half-open success closed the circuit before the configured threshold")
	}
	breaker.RecordSuccess()
	if breaker.State() != StateClosed {
		t.Fatalf("state after required half-open successes = %q; want closed", breaker.State())
	}
}

func TestCircuitBreakerResetDiscardsInFlightResult(t *testing.T) {
	var now atomic.Int64
	breaker := NewCircuitBreaker(WithFailureThreshold(1), WithBreakerClock(now.Load))
	started := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = breaker.Execute(func() (bool, error) {
			close(started)
			<-finish
			return false, nil
		})
	}()
	<-started
	breaker.Reset()
	close(finish)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("in-flight operation did not finish")
	}
	if got := breaker.State(); got != StateClosed {
		t.Fatalf("state after reset and stale failure = %q, want %q", got, StateClosed)
	}
	if failures, successes := breaker.Counts(); failures != 0 || successes != 0 {
		t.Fatalf("counts after stale result = (%d, %d), want (0, 0)", failures, successes)
	}
}
