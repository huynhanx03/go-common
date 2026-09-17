package lifecycle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testComponent struct {
	name     string
	run      func(context.Context) error
	shutdown func(context.Context) error
}

func (component testComponent) Name() string { return component.name }

func (component testComponent) Run(ctx context.Context) error {
	if component.run == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return component.run(ctx)
}

func (component testComponent) Shutdown(ctx context.Context) error {
	if component.shutdown == nil {
		return nil
	}
	return component.shutdown(ctx)
}

func newTestRunner(t *testing.T, options Options) *Runner {
	t.Helper()
	runner, err := New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

func TestRunnerStartsConcurrentlyAndShutsDownInReverseOrder(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, Options{ShutdownTimeout: time.Second})
	started := make(chan string, 3)
	var mu sync.Mutex
	var shutdownOrder []string

	for _, name := range []string{"database", "worker", "http"} {
		name := name
		component := testComponent{
			name: name,
			run: func(ctx context.Context) error {
				started <- name
				<-ctx.Done()
				return ctx.Err()
			},
			shutdown: func(context.Context) error {
				mu.Lock()
				shutdownOrder = append(shutdownOrder, name)
				mu.Unlock()
				return nil
			},
		}
		if err := runner.Add(component, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
			t.Fatalf("Add(%s): %v", name, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runner.Run(ctx) }()

	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("components did not start concurrently")
		}
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run after caller cancellation = %v, want nil", err)
	}

	mu.Lock()
	got := strings.Join(shutdownOrder, ",")
	mu.Unlock()
	if got != "http,worker,database" {
		t.Fatalf("shutdown order = %q, want reverse registration order", got)
	}
}

func TestRunnerRequiredExitCancelsSiblingsAndPreservesPrimaryError(t *testing.T) {
	t.Parallel()

	primary := errors.New("listener failed")
	shutdownFailure := errors.New("close failed")
	runner := newTestRunner(t, Options{ShutdownTimeout: time.Second})
	release := make(chan struct{})
	siblingCancelled := make(chan struct{})

	if err := runner.Add(testComponent{
		name: "listener",
		run: func(context.Context) error {
			<-release
			return primary
		},
		shutdown: func(context.Context) error { return shutdownFailure },
	}, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
		t.Fatalf("Add listener: %v", err)
	}
	if err := runner.Add(testComponent{
		name: "worker",
		run: func(ctx context.Context) error {
			<-ctx.Done()
			close(siblingCancelled)
			return ctx.Err()
		},
	}, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
		t.Fatalf("Add worker: %v", err)
	}

	close(release)
	err := runner.Run(context.Background())
	if !errors.Is(err, primary) {
		t.Fatalf("Run error %v does not preserve primary error", err)
	}
	if !errors.Is(err, shutdownFailure) {
		t.Fatalf("Run error %v does not aggregate shutdown error", err)
	}
	var componentError *ComponentError
	if !errors.As(err, &componentError) || componentError.Component != "listener" || componentError.Operation != "run" {
		t.Fatalf("Run error does not identify failed component: %v", err)
	}
	select {
	case <-siblingCancelled:
	default:
		t.Fatal("required component exit did not cancel sibling")
	}
}

func TestRunnerStartsShutdownBeforeWaitingForRunGoroutines(t *testing.T) {
	t.Parallel()

	primary := errors.New("listener failed")
	runner := newTestRunner(t, Options{ShutdownTimeout: time.Second})
	blockingRunStarted := make(chan struct{})
	shutdownCalled := make(chan struct{})
	releaseRun := make(chan struct{})
	var releaseOnce sync.Once
	unblockRun := func() {
		releaseOnce.Do(func() { close(releaseRun) })
	}
	t.Cleanup(unblockRun)

	if err := runner.Add(testComponent{
		name: "listener",
		run: func(context.Context) error {
			<-blockingRunStarted
			return primary
		},
	}, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
		t.Fatalf("Add listener: %v", err)
	}
	if err := runner.Add(testComponent{
		name: "worker",
		run: func(context.Context) error {
			close(blockingRunStarted)
			<-releaseRun // Models a Run loop that is released by Shutdown.
			return nil
		},
		shutdown: func(context.Context) error {
			close(shutdownCalled)
			unblockRun()
			return nil
		},
	}, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
		t.Fatalf("Add worker: %v", err)
	}

	result := make(chan error, 1)
	go func() { result <- runner.Run(context.Background()) }()

	select {
	case err := <-result:
		if !errors.Is(err, primary) {
			t.Fatalf("Run error %v does not preserve primary error", err)
		}
		select {
		case <-shutdownCalled:
		default:
			t.Fatal("Run returned without invoking component shutdown")
		}
	case <-time.After(300 * time.Millisecond):
		// Unblock the old, incorrect wait-before-shutdown ordering so this
		// regression test never leaves a goroutine behind when it fails.
		unblockRun()
		select {
		case <-result:
		case <-time.After(time.Second):
		}
		t.Fatal("Runner waited for Run to exit before invoking Shutdown")
	}
}

func TestRunnerBoundsRunDrainWhenComponentViolatesCancellationContract(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, Options{ShutdownTimeout: 40 * time.Millisecond})
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)

	if err := runner.Add(testComponent{
		name: "stuck",
		run: func(context.Context) error {
			close(started)
			<-release
			return nil
		},
	}, ComponentOptions{Required: true, ShutdownTimeout: 20 * time.Millisecond}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runner.Run(ctx) }()
	<-started
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run error = %v, want bounded drain deadline", err)
		}
		var componentError *ComponentError
		if !errors.As(err, &componentError) || componentError.Component != "stuck" || componentError.Operation != "drain" {
			t.Fatalf("Run error does not identify undrained component: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		unblock()
		select {
		case <-result:
		case <-time.After(time.Second):
		}
		t.Fatal("Runner did not bound a component Run method that ignored cancellation")
	}
}

func TestRunnerOptionalExitDoesNotCancelApplication(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, Options{ShutdownTimeout: time.Second})
	optionalExited := make(chan struct{})
	requiredCancelled := make(chan struct{})

	if err := runner.Add(testComponent{
		name: "optional-metrics",
		run: func(context.Context) error {
			close(optionalExited)
			return errors.New("metrics unavailable")
		},
	}, ComponentOptions{Required: false, ShutdownTimeout: time.Second}); err != nil {
		t.Fatalf("Add optional: %v", err)
	}
	if err := runner.Add(testComponent{
		name: "api",
		run: func(ctx context.Context) error {
			<-ctx.Done()
			close(requiredCancelled)
			return ctx.Err()
		},
	}, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
		t.Fatalf("Add api: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runner.Run(ctx) }()
	<-optionalExited
	select {
	case err := <-result:
		t.Fatalf("optional failure stopped runner: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-requiredCancelled:
		t.Fatal("optional failure cancelled required sibling")
	default:
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run = %v, want nil after caller cancellation", err)
	}
}

func TestRunnerContainsPanics(t *testing.T) {
	t.Parallel()

	t.Run("run panic", func(t *testing.T) {
		runner := newTestRunner(t, Options{ShutdownTimeout: time.Second})
		const secret = "credential=do-not-log"
		if err := runner.Add(testComponent{
			name: "panic-run",
			run:  func(context.Context) error { panic(secret) },
		}, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		err := runner.Run(context.Background())
		if !errors.Is(err, ErrComponentPanic) || !strings.Contains(err.Error(), "panic-run") {
			t.Fatalf("Run panic error = %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Run panic error exposed panic value: %v", err)
		}
		if !strings.Contains(err.Error(), "panic_type=string") {
			t.Fatalf("Run panic error omitted safe panic type: %v", err)
		}
	})

	t.Run("shutdown panic", func(t *testing.T) {
		runner := newTestRunner(t, Options{ShutdownTimeout: time.Second})
		if err := runner.Add(testComponent{
			name:     "panic-shutdown",
			shutdown: func(context.Context) error { panic("boom") },
		}, ComponentOptions{Required: true, ShutdownTimeout: time.Second}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		err := runner.Shutdown(context.Background())
		if !errors.Is(err, ErrComponentPanic) || !strings.Contains(err.Error(), "panic-shutdown") {
			t.Fatalf("Shutdown panic error = %v", err)
		}
	})
}

func TestRunnerShutdownIsBoundedAndIdempotent(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, Options{ShutdownTimeout: 80 * time.Millisecond})
	var calls atomic.Int32
	if err := runner.Add(testComponent{
		name: "slow",
		shutdown: func(ctx context.Context) error {
			calls.Add(1)
			<-ctx.Done()
			return ctx.Err()
		},
	}, ComponentOptions{Required: true, ShutdownTimeout: 30 * time.Millisecond}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	start := time.Now()
	first := runner.Shutdown(context.Background())
	elapsed := time.Since(start)
	if !errors.Is(first, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline exceeded", first)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("Shutdown exceeded total bound: %v", elapsed)
	}
	second := runner.Shutdown(context.Background())
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("repeated Shutdown = %v, want cached %v", second, first)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Shutdown callback calls = %d, want 1", got)
	}
}

func TestRunnerValidatesRegistrationAndState(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{ShutdownTimeout: -time.Second}); err == nil {
		t.Fatal("New accepted negative shutdown timeout")
	}
	runner := newTestRunner(t, Options{})
	component := testComponent{name: "api"}
	if err := runner.Add(component, ComponentOptions{ShutdownTimeout: time.Second}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := runner.Add(component, ComponentOptions{ShutdownTimeout: time.Second}); !errors.Is(err, ErrDuplicateComponent) {
		t.Fatalf("duplicate Add error = %v", err)
	}
	if err := runner.Add(testComponent{name: "bad name"}, ComponentOptions{ShutdownTimeout: time.Second}); !errors.Is(err, ErrInvalidComponent) {
		t.Fatalf("invalid name Add error = %v", err)
	}
	if err := runner.Add(testComponent{name: "negative"}, ComponentOptions{ShutdownTimeout: -time.Second}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("negative timeout Add error = %v", err)
	}

	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := runner.Add(testComponent{name: "late"}, ComponentOptions{ShutdownTimeout: time.Second}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Add after stop error = %v", err)
	}
	if err := runner.Run(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Run after stop error = %v", err)
	}
}

func TestRunnerConcurrentShutdownAndEmptyRun(t *testing.T) {
	t.Parallel()

	t.Run("concurrent callers", func(t *testing.T) {
		runner := newTestRunner(t, Options{})
		entered := make(chan struct{})
		release := make(chan struct{})
		if err := runner.Add(testComponent{
			name: "blocking-shutdown",
			shutdown: func(context.Context) error {
				close(entered)
				<-release
				return nil
			},
		}, ComponentOptions{ShutdownTimeout: time.Second}); err != nil {
			t.Fatalf("Add: %v", err)
		}

		first := make(chan error, 1)
		go func() { first <- runner.Shutdown(context.Background()) }()
		<-entered

		waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		if err := runner.Shutdown(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("concurrent waiter error = %v, want its deadline", err)
		}
		close(release)
		if err := <-first; err != nil {
			t.Fatalf("first Shutdown: %v", err)
		}
		if err := runner.Shutdown(nil); err != nil {
			t.Fatalf("cached Shutdown: %v", err)
		}
	})

	t.Run("empty runner waits", func(t *testing.T) {
		runner := newTestRunner(t, Options{})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- runner.Run(ctx) }()
		select {
		case err := <-result:
			t.Fatalf("empty Run returned before cancellation: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		cancel()
		if err := <-result; err != nil {
			t.Fatalf("empty Run after cancellation = %v", err)
		}
	})
}

func TestRunnerRejectsNilPanickingAndExcessComponents(t *testing.T) {
	t.Parallel()

	var nilRunner *Runner
	if err := nilRunner.Add(testComponent{name: "component"}, ComponentOptions{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("nil Add error = %v", err)
	}
	if err := nilRunner.Run(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("nil Run error = %v", err)
	}
	if err := nilRunner.Shutdown(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("nil Shutdown error = %v", err)
	}

	var typedNil *pointerComponent
	runner := newTestRunner(t, Options{MaxComponents: 1})
	if err := runner.Add(typedNil, ComponentOptions{}); !errors.Is(err, ErrInvalidComponent) {
		t.Fatalf("typed nil Add error = %v", err)
	}
	if err := runner.Add(panickingNameComponent{}, ComponentOptions{}); !errors.Is(err, ErrComponentPanic) {
		t.Fatalf("panicking Name error = %v", err)
	}
	if err := runner.Add(testComponent{name: "first"}, ComponentOptions{}); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	if err := runner.Add(testComponent{name: "second"}, ComponentOptions{}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("over-capacity Add error = %v", err)
	}
}

type pointerComponent struct{}

func (*pointerComponent) Name() string                   { return "pointer" }
func (*pointerComponent) Run(context.Context) error      { return nil }
func (*pointerComponent) Shutdown(context.Context) error { return nil }

type panickingNameComponent struct{}

func (panickingNameComponent) Name() string                   { panic("name") }
func (panickingNameComponent) Run(context.Context) error      { return nil }
func (panickingNameComponent) Shutdown(context.Context) error { return nil }
