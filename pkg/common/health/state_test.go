package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestGateClosesOpensAndKeepsDiagnosticsPrivate(t *testing.T) {
	var gate Gate
	if err := gate.Check(context.Background()); !errors.Is(err, ErrGateClosed) {
		t.Fatalf("zero-value gate check = %v, want closed", err)
	}

	gate.Close("database credential rejected")
	if gate.IsOpen() || gate.Reason() == "" {
		t.Fatalf("closed gate state = open:%v reason:%q", gate.IsOpen(), gate.Reason())
	}
	if err := gate.Check(context.Background()); !errors.Is(err, ErrGateClosed) || strings.Contains(err.Error(), "credential") {
		t.Fatalf("closed gate exposed its private reason: %v", err)
	}

	gate.Open()
	if !gate.IsOpen() || gate.Reason() != "" {
		t.Fatalf("opened gate state = open:%v reason:%q", gate.IsOpen(), gate.Reason())
	}
	if err := gate.Check(context.Background()); err != nil {
		t.Fatalf("open gate check = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled gate check = %v, want context cancellation", err)
	}
}

func TestGateBoundsAndRepairsPrivateReason(t *testing.T) {
	var gate Gate
	gate.Close(strings.Repeat("x", maxGateReasonBytes) + string([]byte{0xff}))
	if got := gate.Reason(); len(got) > maxGateReasonBytes || !utf8.ValidString(got) {
		t.Fatalf("reason is not bounded valid UTF-8: bytes=%d valid=%v", len(got), utf8.ValidString(got))
	}
}

func TestHeartbeatTracksFreshnessWithoutMovingBackward(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.FixedZone("test", 3600))
	heartbeat, err := NewHeartbeat(time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHeartbeat: %v", err)
	}
	if err := heartbeat.Check(context.Background()); !errors.Is(err, ErrHeartbeatStale) {
		t.Fatalf("heartbeat before first beat = %v, want stale", err)
	}

	heartbeat.Beat()
	firstBeat := heartbeat.LastBeat()
	if firstBeat.Location() != time.UTC {
		t.Fatalf("heartbeat timestamp location = %v, want UTC", firstBeat.Location())
	}
	if err := heartbeat.Check(context.Background()); err != nil {
		t.Fatalf("fresh heartbeat check = %v", err)
	}

	now = now.Add(-time.Second)
	heartbeat.Beat()
	if got := heartbeat.LastBeat(); !got.Equal(firstBeat) {
		t.Fatalf("backward clock moved heartbeat from %v to %v", firstBeat, got)
	}
	if err := heartbeat.Check(context.Background()); !errors.Is(err, ErrHeartbeatStale) {
		t.Fatalf("future heartbeat check = %v, want stale", err)
	}

	now = now.Add(2*time.Second + time.Minute)
	if err := heartbeat.Check(context.Background()); !errors.Is(err, ErrHeartbeatStale) {
		t.Fatalf("expired heartbeat check = %v, want stale", err)
	}
}

func TestHeartbeatRejectsInvalidAgeAndCanceledContext(t *testing.T) {
	for _, age := range []time.Duration{0, -time.Second, maximumHeartbeatAge + time.Nanosecond} {
		if _, err := NewHeartbeat(age, nil); !errors.Is(err, ErrInvalidOptions) {
			t.Errorf("NewHeartbeat(%s) error = %v, want invalid options", age, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	heartbeat, err := NewHeartbeat(time.Second, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat.Beat()
	cancel()
	if err := heartbeat.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled heartbeat check = %v, want context cancellation", err)
	}
	if err := CheckerFunc(nil).Check(context.Background()); !errors.Is(err, ErrInvalidCheck) {
		t.Fatalf("nil checker function error = %v, want invalid check", err)
	}
}
