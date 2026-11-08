package temporal

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"

	"github.com/huynhanx03/go-common/pkg/common/lifecycle"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestWorkerLifecycle(t *testing.T) {
	t.Run("constructs without polling and preserves SDK options", func(t *testing.T) {
		applicationInterceptor := &testWorkerInterceptor{}
		native := newFakeSDKWorker()
		factory := &fakeWorkerFactory{native: native}
		applicationFatal := func(error) {}
		options := worker.Options{
			Interceptors:                       []interceptor.WorkerInterceptor{applicationInterceptor},
			OnFatalError:                       applicationFatal,
			WorkerStopTimeout:                  5 * time.Second,
			MaxConcurrentActivityExecutionSize: 17,
		}
		created, err := newWorker(testTemporalClient(), settings.TemporalWorker{Name: "emails", TaskQueue: "email-tasks"}, options, factory.create)
		if err != nil {
			t.Fatalf("newWorker() error = %v", err)
		}
		if native.starts.Load() != 0 {
			t.Fatal("constructing worker started polling")
		}
		if created.Name() != "emails" || factory.taskQueue != "email-tasks" {
			t.Fatalf("worker name/task queue = %q/%q", created.Name(), factory.taskQueue)
		}
		if factory.options.WorkerStopTimeout != 5*time.Second || factory.options.MaxConcurrentActivityExecutionSize != 17 {
			t.Fatalf("native worker options were not preserved: %#v", factory.options)
		}
		if factory.options.OnFatalError == nil || len(factory.options.Interceptors) != 2 || factory.options.Interceptors[0] != applicationInterceptor {
			t.Fatalf("common worker hooks did not compose with application options: %#v", factory.options)
		}
		if created.SDK() != native {
			t.Fatal("SDK() did not return the native worker")
		}
	})

	t.Run("Run starts once and cancellation shuts down the borrowed worker", func(t *testing.T) {
		native := newFakeSDKWorker()
		created, err := newWorker(testTemporalClient(), validWorkerSettings(), worker.Options{}, (&fakeWorkerFactory{native: native}).create)
		if err != nil {
			t.Fatalf("newWorker() error = %v", err)
		}
		if created.shutdownTimeout != defaultShutdownTimeout {
			t.Fatalf("default shutdown timeout = %s; want %s", created.shutdownTimeout, defaultShutdownTimeout)
		}
		ctx, cancel := context.WithCancel(context.Background())
		runResult := make(chan error, 1)
		go func() { runResult <- created.Run(ctx) }()
		waitForSignal(t, native.started)
		if native.starts.Load() != 1 {
			t.Fatalf("Start() calls = %d; want one", native.starts.Load())
		}
		cancel()
		if err := receiveError(t, runResult); err != nil {
			t.Fatalf("Run() after cancellation = %v; want graceful exit", err)
		}
		if native.stops.Load() != 1 {
			t.Fatalf("Stop() calls = %d; want one", native.stops.Load())
		}
		if err := created.Shutdown(context.Background()); err != nil {
			t.Fatalf("repeated Shutdown() = %v", err)
		}
		if err := created.Run(context.Background()); !errors.Is(err, ErrInvalidWorkerState) {
			t.Fatalf("Run() after shutdown = %v; want ErrInvalidWorkerState", err)
		}
	})

	t.Run("startup failure is returned without stopping an unstarted worker", func(t *testing.T) {
		startErr := errors.New("poller startup failed")
		native := newFakeSDKWorker()
		native.startErr = startErr
		created, err := newWorker(testTemporalClient(), validWorkerSettings(), worker.Options{}, (&fakeWorkerFactory{native: native}).create)
		if err != nil {
			t.Fatalf("newWorker() error = %v", err)
		}
		if err := created.Run(context.Background()); !errors.Is(err, startErr) {
			t.Fatalf("Run() error = %v; want startup error", err)
		}
		if native.stops.Load() != 0 {
			t.Fatalf("Stop() calls = %d; want zero before successful start", native.stops.Load())
		}
	})

	t.Run("shutdown before Run prevents polling", func(t *testing.T) {
		native := newFakeSDKWorker()
		created, err := newWorker(testTemporalClient(), validWorkerSettings(), worker.Options{}, (&fakeWorkerFactory{native: native}).create)
		if err != nil {
			t.Fatalf("newWorker() error = %v", err)
		}
		if err := created.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown() = %v", err)
		}
		if err := created.Run(context.Background()); !errors.Is(err, ErrInvalidWorkerState) {
			t.Fatalf("Run() after pre-start Shutdown() = %v; want ErrInvalidWorkerState", err)
		}
		if native.starts.Load() != 0 || native.stops.Load() != 0 {
			t.Fatalf("unstarted native worker calls = start %d, stop %d", native.starts.Load(), native.stops.Load())
		}
	})

	t.Run("timed out shutdown shares stop and a later caller waits for completion", func(t *testing.T) {
		native := newFakeSDKWorker()
		native.stopEntered = make(chan struct{})
		native.releaseStop = make(chan struct{})
		created, err := newWorker(testTemporalClient(), validWorkerSettings(), worker.Options{}, (&fakeWorkerFactory{native: native}).create)
		if err != nil {
			t.Fatalf("newWorker() error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		runResult := make(chan error, 1)
		go func() { runResult <- created.Run(ctx) }()
		waitForSignal(t, native.started)

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer shutdownCancel()
		if err := created.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first Shutdown() = %v; want context deadline", err)
		}
		waitForSignal(t, native.stopEntered)
		secondResult := make(chan error, 1)
		go func() { secondResult <- created.Shutdown(context.Background()) }()
		close(native.releaseStop)
		if err := receiveError(t, secondResult); err != nil {
			t.Fatalf("second Shutdown() = %v", err)
		}
		cancel()
		if err := receiveError(t, runResult); err != nil {
			t.Fatalf("Run() after shared shutdown = %v", err)
		}
		if native.stops.Load() != 1 {
			t.Fatalf("Stop() calls = %d; want one shared stop", native.stops.Load())
		}
	})

	t.Run("fatal SDK error invokes application callback and releases Run", func(t *testing.T) {
		native := newFakeSDKWorker()
		factory := &fakeWorkerFactory{native: native}
		fatalErr := errors.New("worker poll failed")
		var callbackErr atomic.Pointer[error]
		options := worker.Options{OnFatalError: func(err error) { callbackErr.Store(&err) }}
		created, err := newWorker(testTemporalClient(), validWorkerSettings(), options, factory.create)
		if err != nil {
			t.Fatalf("newWorker() error = %v", err)
		}
		runResult := make(chan error, 1)
		go func() { runResult <- created.Run(context.Background()) }()
		waitForSignal(t, native.started)
		factory.options.OnFatalError(fatalErr)
		if err := receiveError(t, runResult); !errors.Is(err, fatalErr) {
			t.Fatalf("Run() error = %v; want fatal SDK error", err)
		}
		if callbackErr.Load() == nil || *callbackErr.Load() != fatalErr {
			t.Fatalf("application fatal callback error = %v; want %v", callbackErr.Load(), fatalErr)
		}
		if native.stops.Load() != 1 {
			t.Fatalf("Stop() calls = %d; want one", native.stops.Load())
		}
	})

	t.Run("rejects contradictory shutdown budgets", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			config  settings.TemporalWorker
			options worker.Options
		}{
			{name: "negative configured budget", config: settings.TemporalWorker{Name: "w", TaskQueue: "q", ShutdownTimeoutSeconds: -1}},
			{name: "configured budget exceeds the supported limit", config: settings.TemporalWorker{Name: "w", TaskQueue: "q", ShutdownTimeoutSeconds: int(maximumTemporalTimeout/time.Second) + 1}},
			{name: "negative SDK stop timeout", config: validWorkerSettings(), options: worker.Options{WorkerStopTimeout: -time.Second}},
			{name: "SDK stop exceeds wrapper budget", config: settings.TemporalWorker{Name: "w", TaskQueue: "q", ShutdownTimeoutSeconds: 2}, options: worker.Options{WorkerStopTimeout: 3 * time.Second}},
			{name: "missing worker name", config: settings.TemporalWorker{TaskQueue: "q"}},
			{name: "missing task queue", config: settings.TemporalWorker{Name: "w"}},
		} {
			t.Run(test.name, func(t *testing.T) {
				_, err := newWorker(testTemporalClient(), test.config, test.options, (&fakeWorkerFactory{native: newFakeSDKWorker()}).create)
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("newWorker() error = %v; want ErrInvalidConfig", err)
				}
			})
		}
	})
}

var _ lifecycle.Component = (*Worker)(nil)

type fakeWorkerFactory struct {
	native    *fakeSDKWorker
	options   worker.Options
	taskQueue string
}

func (f *fakeWorkerFactory) create(_ sdkclient.Client, taskQueue string, options worker.Options) worker.Worker {
	f.taskQueue = taskQueue
	f.options = options
	return f.native
}

type fakeSDKWorker struct {
	worker.Worker
	starts      atomic.Int32
	stops       atomic.Int32
	startErr    error
	started     chan struct{}
	stopEntered chan struct{}
	releaseStop chan struct{}
	stopOnce    sync.Once
}

func newFakeSDKWorker() *fakeSDKWorker {
	return &fakeSDKWorker{started: make(chan struct{})}
}

func (w *fakeSDKWorker) Start() error {
	w.starts.Add(1)
	if w.started != nil {
		close(w.started)
	}
	return w.startErr
}

func (w *fakeSDKWorker) Stop() {
	w.stops.Add(1)
	if w.stopEntered != nil {
		w.stopOnce.Do(func() { close(w.stopEntered) })
	}
	if w.releaseStop != nil {
		<-w.releaseStop
	}
}

type testWorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func testTemporalClient() *Client { return &Client{sdk: &testSDKClient{}} }

func validWorkerSettings() settings.TemporalWorker {
	return settings.TemporalWorker{Name: "worker", TaskQueue: "tasks"}
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker state transition")
	}
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker result")
		return nil
	}
}
