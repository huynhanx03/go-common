package health

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type checkerFunc func(context.Context) error

func (fn checkerFunc) Check(ctx context.Context) error { return fn(ctx) }

func newTestRegistry(t *testing.T, options Options) *Registry {
	t.Helper()
	registry, err := New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return registry
}

func TestRegistryCriticalAndOptionalReadiness(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t, Options{MaxConcurrent: 2})
	failure := errors.New("database password=secret")
	for _, registration := range []struct {
		name     string
		critical bool
		err      error
	}{
		{name: "cache", critical: false, err: failure},
		{name: "database", critical: true},
	} {
		if err := registry.Register(registration.name, checkerFunc(func(context.Context) error {
			return registration.err
		}), CheckOptions{
			Probes:    ProbeReadiness,
			Critical:  registration.critical,
			Timeout:   time.Second,
			ErrorCode: registration.name + "_unavailable",
		}); err != nil {
			t.Fatalf("Register(%s): %v", registration.name, err)
		}
	}

	report := registry.CheckReadiness(context.Background())
	if !report.Healthy {
		t.Fatal("optional failure made readiness false")
	}
	if len(report.Statuses) != 2 || report.Statuses[0].Name != "cache" || report.Statuses[1].Name != "database" {
		t.Fatalf("statuses are not stable sorted output: %+v", report.Statuses)
	}
	if report.Statuses[0].Healthy || report.Statuses[0].ErrorCode != "cache_unavailable" {
		t.Fatalf("optional failure status = %+v", report.Statuses[0])
	}

	critical := newTestRegistry(t, Options{})
	if err := critical.Register("database", checkerFunc(func(context.Context) error { return failure }), CheckOptions{
		Probes: ProbeReadiness, Critical: true, Timeout: time.Second, ErrorCode: "database_unavailable",
	}); err != nil {
		t.Fatalf("Register critical: %v", err)
	}
	if got := critical.CheckReadiness(context.Background()); got.Healthy {
		t.Fatalf("critical failure left readiness true: %+v", got)
	}
}

func TestRegistryBoundsConcurrency(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t, Options{MaxConcurrent: 2})
	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})

	for index := 0; index < 8; index++ {
		name := "check-" + string(rune('a'+index))
		if err := registry.Register(name, checkerFunc(func(context.Context) error {
			current := active.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			<-release
			active.Add(-1)
			return nil
		}), CheckOptions{
			Probes: ProbeReadiness, Critical: true, Timeout: time.Second, ErrorCode: "unavailable",
		}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}

	result := make(chan Report, 1)
	go func() { result <- registry.CheckReadiness(context.Background()) }()
	deadline := time.After(time.Second)
	for maximum.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("checks did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := maximum.Load(); got > 2 {
		t.Fatalf("concurrent checks = %d, want at most 2", got)
	}
	close(release)
	if report := <-result; !report.Healthy {
		t.Fatalf("healthy checks reported unhealthy: %+v", report)
	}
	if got := maximum.Load(); got != 2 {
		t.Fatalf("max concurrency = %d, want exactly 2", got)
	}
}

func TestRegistryDeadlinePanicAndFakeClock(t *testing.T) {
	t.Parallel()

	var ticks atomic.Int64
	base := time.Unix(100, 0)
	registry := newTestRegistry(t, Options{
		MaxConcurrent: 1,
		Now: func() time.Time {
			return base.Add(time.Duration(ticks.Add(1)-1) * 25 * time.Millisecond)
		},
	})
	if err := registry.Register("deadline", checkerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}), CheckOptions{
		Probes: ProbeReadiness, Critical: true, Timeout: 10 * time.Millisecond, ErrorCode: "deadline_unavailable",
	}); err != nil {
		t.Fatalf("Register deadline: %v", err)
	}
	if err := registry.Register("panic", checkerFunc(func(context.Context) error {
		panic("credential=secret")
	}), CheckOptions{
		Probes: ProbeReadiness, Critical: false, Timeout: time.Second, ErrorCode: "panic_unavailable",
	}); err != nil {
		t.Fatalf("Register panic: %v", err)
	}

	report := registry.CheckReadiness(context.Background())
	if report.Healthy {
		t.Fatal("critical deadline failure left readiness true")
	}
	for _, status := range report.Statuses {
		if status.Healthy {
			t.Fatalf("failed checker marked healthy: %+v", status)
		}
		if status.ErrorCode == "" {
			t.Fatalf("failed checker has no stable code: %+v", status)
		}
		if status.Duration < 0 {
			t.Fatalf("negative duration: %+v", status)
		}
		if status.Duration == 0 {
			t.Fatalf("fake clock duration was not recorded: %+v", status)
		}
	}
}

func TestRegistryValidatesRegistration(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{MaxConcurrent: -1}); err == nil {
		t.Fatal("New accepted negative concurrency")
	}
	if _, err := New(Options{MaxConcurrent: maximumConcurrentChecks + 1}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("New excessive concurrency error = %v, want ErrInvalidOptions", err)
	}
	if _, err := New(Options{MaxChecks: maximumChecks + 1}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("New excessive check count error = %v, want ErrInvalidOptions", err)
	}
	registry := newTestRegistry(t, Options{})
	valid := CheckOptions{Probes: ProbeReadiness, Timeout: time.Second, ErrorCode: "dependency_unavailable"}
	if err := registry.Register("database", checkerFunc(func(context.Context) error { return nil }), valid); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register("database", checkerFunc(func(context.Context) error { return nil }), valid); !errors.Is(err, ErrDuplicateCheck) {
		t.Fatalf("duplicate registration error = %v", err)
	}
	for _, tc := range []struct {
		name    string
		checker Checker
		options CheckOptions
	}{
		{name: "bad name", checker: checkerFunc(func(context.Context) error { return nil }), options: valid},
		{name: "nil", checker: nil, options: valid},
		{name: "timeout", checker: checkerFunc(func(context.Context) error { return nil }), options: CheckOptions{Probes: ProbeReadiness, ErrorCode: "failed"}},
		{name: "probe", checker: checkerFunc(func(context.Context) error { return nil }), options: CheckOptions{Timeout: time.Second, ErrorCode: "failed"}},
		{name: "code", checker: checkerFunc(func(context.Context) error { return nil }), options: CheckOptions{Probes: ProbeReadiness, Timeout: time.Second, ErrorCode: "BAD CODE"}},
	} {
		if err := registry.Register(tc.name, tc.checker, tc.options); !errors.Is(err, ErrInvalidCheck) {
			t.Fatalf("Register(%s) error = %v, want ErrInvalidCheck", tc.name, err)
		}
	}
}
