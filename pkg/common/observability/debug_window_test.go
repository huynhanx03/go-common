package observability

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type debugLevelControlStub struct {
	level        string
	err          error
	panicCurrent bool
}

type ambiguousDebugLevelControl struct {
	level       string
	failChanges bool
	calls       int
}

type failNextDebugLevelControl struct {
	level    string
	failNext bool
}

type synchronizedDebugLevelControl struct {
	mu      sync.Mutex
	level   string
	changes chan string
}

func (control *synchronizedDebugLevelControl) CurrentLevel() string {
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.level
}

func (control *synchronizedDebugLevelControl) SetLevel(level string) error {
	control.mu.Lock()
	control.level = level
	control.mu.Unlock()
	control.changes <- level
	return nil
}

type signalDoneContext struct {
	context.Context
	started chan struct{}
	once    sync.Once
}

func (ctx *signalDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.started) })
	return ctx.Context.Done()
}

func (control *failNextDebugLevelControl) CurrentLevel() string { return control.level }

func (control *failNextDebugLevelControl) SetLevel(level string) error {
	if control.failNext {
		control.failNext = false
		return ErrInvalidDebugWindow
	}
	control.level = level
	return nil
}

func (control *ambiguousDebugLevelControl) CurrentLevel() string { return control.level }

func (control *ambiguousDebugLevelControl) SetLevel(level string) error {
	control.calls++
	if control.failChanges {
		if control.calls == 1 {
			control.level = level
		}
		return ErrInvalidDebugWindow
	}
	control.level = level
	return nil
}

func (control *debugLevelControlStub) CurrentLevel() string {
	if control.panicCurrent {
		panic("credential detail")
	}
	return control.level
}

func (control *debugLevelControlStub) SetLevel(level string) error {
	if control.err != nil {
		return control.err
	}
	control.level = level
	return nil
}

func TestDebugWindowActiveReconcilesExpiredLeaseBeforeProjection(t *testing.T) {
	clock := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	control := &debugLevelControlStub{level: "info"}
	window, err := NewDebugWindow(control, "info", time.Hour, func() time.Time {
		return clock
	})
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	t.Cleanup(func() { _ = window.Shutdown(context.Background()) })
	request := DebugRequest{
		Key: "request-1", Component: "api", Level: "debug",
		Duration: time.Minute, Reason: "incident investigation",
	}
	lease, err := window.Activate(context.Background(), request)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	clock = lease.ExpiresAt.Add(time.Nanosecond)

	if activeLease, active := window.Active(); active || activeLease != (DebugLease{}) {
		t.Fatalf("Active after expiry = (%+v, %t), want zero,false", activeLease, active)
	}
	if control.CurrentLevel() != "info" {
		t.Fatalf("level after expiry = %q, want info", control.CurrentLevel())
	}
	replayed, err := window.Activate(context.Background(), request)
	if err != nil || replayed != lease {
		t.Fatalf("expired idempotency replay = (%+v, %v), want original lease", replayed, err)
	}
	if activeLease, active := window.Active(); active || activeLease != (DebugLease{}) {
		t.Fatalf("Active after expired replay = (%+v, %t), want zero,false", activeLease, active)
	}
}

func TestDebugWindowActiveKeepsFailedExpiryRestoreVisible(t *testing.T) {
	clock := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	control := &debugLevelControlStub{level: "info"}
	window, err := NewDebugWindow(control, "info", time.Hour, func() time.Time {
		return clock
	})
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	t.Cleanup(func() {
		control.err = nil
		_ = window.Shutdown(context.Background())
	})
	lease, err := window.Activate(context.Background(), DebugRequest{
		Key: "request-1", Component: "api", Level: "debug",
		Duration: time.Minute, Reason: "incident investigation",
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	clock = lease.ExpiresAt.Add(time.Nanosecond)
	control.err = ErrInvalidDebugWindow

	activeLease, active := window.Active()
	if !active || activeLease != lease {
		t.Fatalf("Active after failed restore = (%+v, %t), want original,true", activeLease, active)
	}
}

func TestDebugWindowShutdownKeepsLeaseVisibleUntilBaselineRestoreSucceeds(t *testing.T) {
	clock := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	control := &debugLevelControlStub{level: "info"}
	window, err := NewDebugWindow(control, "info", time.Hour, func() time.Time {
		return clock
	})
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	lease, err := window.Activate(context.Background(), DebugRequest{
		Key: "request-1", Component: "api", Level: "debug",
		Duration: time.Minute, Reason: "incident investigation",
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	control.err = ErrInvalidDebugWindow

	if err := window.Shutdown(context.Background()); err == nil {
		t.Fatal("Shutdown succeeded despite baseline restore failure")
	}
	activeLease, active := window.Active()
	if !active || activeLease != lease || control.CurrentLevel() != "debug" {
		t.Fatalf(
			"state after failed shutdown = (%+v, %t, %q), want original,true,debug",
			activeLease,
			active,
			control.CurrentLevel(),
		)
	}

	control.err = nil
	if err := window.Shutdown(context.Background()); err != nil {
		t.Fatalf("retry Shutdown: %v", err)
	}
	if activeLease, active := window.Active(); active || activeLease != (DebugLease{}) {
		t.Fatalf("state after recovered shutdown = (%+v, %t), want zero,false", activeLease, active)
	}
	if control.CurrentLevel() != "info" {
		t.Fatalf("level after recovered shutdown = %q, want info", control.CurrentLevel())
	}
}

func TestDebugWindowFailedPartialActivationRemainsVisibleUntilRevoked(t *testing.T) {
	clock := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	control := &ambiguousDebugLevelControl{
		level:       "info",
		failChanges: true,
	}
	window, err := NewDebugWindow(control, "info", time.Hour, func() time.Time {
		return clock
	})
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	request := DebugRequest{
		Key: "request-1", Component: "api", Level: "debug",
		Duration: time.Minute, Reason: "incident investigation",
	}
	if _, err := window.Activate(context.Background(), request); !errors.Is(err, ErrDebugLevelControl) {
		t.Fatalf("Activate error = %v, want ErrDebugLevelControl", err)
	}
	lease, active := window.Active()
	if !active || lease.Key != request.Key {
		t.Fatalf("state after partial activation = (%+v, %t), want request lease,true", lease, active)
	}

	control.failChanges = false
	if err := window.Revoke(context.Background(), request.Key); err != nil {
		t.Fatalf("Revoke after control recovery: %v", err)
	}
	if lease, active := window.Active(); active || lease != (DebugLease{}) {
		t.Fatalf("state after revoke = (%+v, %t), want zero,false", lease, active)
	}
	if control.CurrentLevel() != "info" {
		t.Fatalf("level after revoke = %q, want info", control.CurrentLevel())
	}
}

func TestDebugWindowFailedReplacementClearsPreviousLeaseAfterSafeRestore(t *testing.T) {
	clock := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	control := &failNextDebugLevelControl{level: "info"}
	window, err := NewDebugWindow(control, "info", time.Hour, func() time.Time {
		return clock
	})
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	if _, err := window.Activate(context.Background(), DebugRequest{
		Key: "request-1", Component: "api", Level: "debug",
		Duration: time.Minute, Reason: "first incident",
	}); err != nil {
		t.Fatalf("first Activate: %v", err)
	}
	control.failNext = true
	if _, err := window.Activate(context.Background(), DebugRequest{
		Key: "request-2", Component: "api", Level: "trace",
		Duration: time.Minute, Reason: "second incident",
	}); err == nil {
		t.Fatal("replacement Activate succeeded despite level-control failure")
	}
	if lease, active := window.Active(); active || lease != (DebugLease{}) {
		t.Fatalf("state after safe replacement rollback = (%+v, %t), want zero,false", lease, active)
	}
	if control.CurrentLevel() != "info" {
		t.Fatalf("level after safe replacement rollback = %q, want info", control.CurrentLevel())
	}
}

func TestDebugWindowCheckTracksBaselineLeaseAndControlDrift(t *testing.T) {
	clock := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	control := &debugLevelControlStub{level: "info"}
	window, err := NewDebugWindow(control, "info", time.Hour, func() time.Time { return clock })
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	t.Cleanup(func() { _ = window.Shutdown(context.Background()) })
	if err := window.Check(context.Background()); err != nil {
		t.Fatalf("baseline Check: %v", err)
	}
	if _, err := window.Activate(context.Background(), DebugRequest{
		Key: "request-1", Component: "api", Level: "debug", Duration: time.Minute, Reason: "investigation",
	}); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := window.Check(context.Background()); err != nil {
		t.Fatalf("active Check: %v", err)
	}
	control.level = "trace"
	if err := window.Check(context.Background()); !errors.Is(err, ErrDebugLevelMismatch) {
		t.Fatalf("drift Check = %v, want mismatch classification", err)
	}
	control.level = "debug"
	control.panicCurrent = true
	if err := window.Check(context.Background()); !errors.Is(err, ErrDebugLevelControl) || strings.Contains(err.Error(), "credential") {
		t.Fatalf("failed control Check leaked implementation detail: %v", err)
	}
	control.panicCurrent = false
	if err := window.Check(nil); !errors.Is(err, ErrInvalidDebugWindow) {
		t.Fatalf("Check(nil) = %v, want invalid window", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := window.Check(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check(canceled) = %v, want context cancellation", err)
	}
	if err := window.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := window.Check(context.Background()); !errors.Is(err, ErrInvalidDebugWindow) {
		t.Fatalf("Check after shutdown = %v, want closed controller", err)
	}
}

func TestDebugWindowIdempotencyAndLeaseOwnership(t *testing.T) {
	control := &debugLevelControlStub{level: "info"}
	window, err := NewDebugWindow(control, "info", time.Hour, nil)
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	t.Cleanup(func() { _ = window.Shutdown(context.Background()) })
	request := DebugRequest{
		Key: "request-1", Component: "api", Level: "debug", Duration: time.Minute, Reason: "investigation",
	}
	first, err := window.Activate(context.Background(), request)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	replay, err := window.Activate(context.Background(), request)
	if err != nil || replay != first {
		t.Fatalf("same-key replay = (%+v, %v), want original lease", replay, err)
	}
	conflict := request
	conflict.Level = "trace"
	if _, err := window.Activate(context.Background(), conflict); !errors.Is(err, ErrDebugWindowConflict) {
		t.Fatalf("conflicting key reuse = %v, want conflict", err)
	}
	if err := window.Revoke(context.Background(), "another-request"); err != nil {
		t.Fatalf("Revoke by a non-owner: %v", err)
	}
	if active, ok := window.Active(); !ok || active != first {
		t.Fatalf("non-owner revoke changed active lease: (%+v, %t)", active, ok)
	}
	if err := window.Revoke(context.Background(), request.Key); err != nil {
		t.Fatalf("owner Revoke: %v", err)
	}
	if control.CurrentLevel() != "info" {
		t.Fatalf("level after owner revoke = %q, want baseline", control.CurrentLevel())
	}
}

func TestDebugWindowRunRestoresBaselineOnLifecycleCancellation(t *testing.T) {
	control := &debugLevelControlStub{level: "info"}
	window, err := NewDebugWindow(control, "info", time.Hour, nil)
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	if _, err := window.Activate(context.Background(), DebugRequest{
		Key: "request-1", Component: "api", Level: "debug", Duration: time.Minute, Reason: "investigation",
	}); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	base, cancel := context.WithCancel(context.Background())
	ctx := &signalDoneContext{Context: base, started: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- window.Run(ctx) }()
	select {
	case <-ctx.started:
	case <-time.After(time.Second):
		t.Fatal("Run did not wait for lifecycle cancellation")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not restore and return")
	}
	if control.CurrentLevel() != "info" {
		t.Fatalf("level after Run = %q, want baseline", control.CurrentLevel())
	}
	if _, active := window.Active(); active {
		t.Fatal("Run left an active lease after shutdown")
	}
}

func TestDebugWindowAutomaticallyRevertsExpiredOverride(t *testing.T) {
	control := &synchronizedDebugLevelControl{level: "info", changes: make(chan string, 4)}
	window, err := NewDebugWindow(control, "info", time.Second, nil)
	if err != nil {
		t.Fatalf("NewDebugWindow: %v", err)
	}
	t.Cleanup(func() { _ = window.Shutdown(context.Background()) })
	if _, err := window.Activate(context.Background(), DebugRequest{
		Key: "request-1", Component: "api", Level: "debug", Duration: 20 * time.Millisecond, Reason: "investigation",
	}); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	select {
	case level := <-control.changes:
		if level != "debug" {
			t.Fatalf("initial level change = %q, want override", level)
		}
	case <-time.After(time.Second):
		t.Fatal("override was not applied")
	}
	select {
	case level := <-control.changes:
		if level != "info" {
			t.Fatalf("automatic revert level = %q, want baseline", level)
		}
	case <-time.After(time.Second):
		t.Fatal("override did not automatically revert")
	}
	if _, active := window.Active(); active {
		t.Fatal("automatically reverted override is still active")
	}
}

func TestDebugWindowRejectsInvalidRequestsAndControls(t *testing.T) {
	if _, err := NewDebugWindow(nil, "info", time.Minute, nil); !errors.Is(err, ErrInvalidDebugWindow) {
		t.Fatalf("nil control error = %v", err)
	}
	var nilControl *debugLevelControlStub
	if _, err := NewDebugWindow(nilControl, "info", time.Minute, nil); !errors.Is(err, ErrInvalidDebugWindow) {
		t.Fatalf("typed nil control error = %v", err)
	}
	for _, invalid := range []struct {
		baseline string
		maximum  time.Duration
	}{
		{baseline: "invalid level", maximum: time.Minute},
		{baseline: "info", maximum: 0},
		{baseline: "info", maximum: maxDebugDuration + time.Nanosecond},
	} {
		if _, err := NewDebugWindow(&debugLevelControlStub{level: "info"}, invalid.baseline, invalid.maximum, nil); !errors.Is(err, ErrInvalidDebugWindow) {
			t.Errorf("NewDebugWindow(%+v) error = %v", invalid, err)
		}
	}

	window, err := NewDebugWindow(&debugLevelControlStub{level: "info"}, "info", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []DebugRequest{
		{},
		{Key: "request-1", Component: "api", Level: "debug", Duration: time.Minute, Reason: ""},
		{Key: "request-1", Component: "api", Level: "debug", Duration: 2 * time.Minute, Reason: "too long"},
		{Key: "request-1", Component: "api", Level: "debug", Duration: time.Minute, Reason: "bad\nreason"},
	} {
		if _, err := window.Activate(context.Background(), request); !errors.Is(err, ErrInvalidDebugWindow) {
			t.Errorf("Activate(%+v) error = %v, want invalid request", request, err)
		}
	}
	if err := window.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := window.Run(nil); !errors.Is(err, ErrInvalidDebugWindow) {
		t.Fatalf("Run on nil context = %v, want invalid window", err)
	}
}
