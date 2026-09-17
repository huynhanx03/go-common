package observability

import (
	"context"
	"errors"
	"testing"
	"time"
)

type debugLevelControlStub struct {
	level string
	err   error
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

func (control *debugLevelControlStub) CurrentLevel() string { return control.level }

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
