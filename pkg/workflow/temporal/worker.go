package temporal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/huynhanx03/go-common/pkg/common/lifecycle"
	"github.com/huynhanx03/go-common/pkg/settings"
)

type workerState uint8

const (
	workerNew workerState = iota
	workerStarting
	workerRunning
	workerStopping
	workerStopped
)

type workerRuntime interface {
	Start() error
	Stop()
}

// Worker adapts a native Temporal worker to the common lifecycle component.
// It borrows the shared Temporal client; the application closes that client after workers stop.
type Worker struct {
	name            string
	sdk             worker.Worker
	runtime         workerRuntime
	shutdownTimeout time.Duration
	fatal           chan error
	startDone       chan struct{}
	stopDone        chan struct{}

	mu    sync.Mutex
	state workerState
}

// NewWorker creates a worker without starting its pollers.
func NewWorker(c *Client, config settings.TemporalWorker, options worker.Options) (*Worker, error) {
	return newWorker(c, config, options, worker.New)
}

func newWorker(
	c *Client,
	config settings.TemporalWorker,
	options worker.Options,
	factory func(client.Client, string, worker.Options) worker.Worker,
) (*Worker, error) {
	if strings.TrimSpace(config.Name) == "" || strings.TrimSpace(config.TaskQueue) == "" ||
		config.ShutdownTimeoutSeconds < 0 || config.ShutdownTimeoutSeconds > int(maximumTemporalTimeout/time.Second) {
		return nil, ErrInvalidConfig
	}
	shutdownTimeout := defaultShutdownTimeout
	if config.ShutdownTimeoutSeconds > 0 {
		shutdownTimeout = time.Duration(config.ShutdownTimeoutSeconds) * time.Second
	}
	if options.WorkerStopTimeout < 0 || options.WorkerStopTimeout > shutdownTimeout {
		return nil, ErrInvalidConfig
	}
	if options.WorkerStopTimeout == 0 {
		options.WorkerStopTimeout = shutdownTimeout
	}

	fatal := make(chan error, 1)
	applicationFatal := options.OnFatalError
	options.OnFatalError = func(err error) {
		select {
		case fatal <- err:
		default:
		}
		if applicationFatal != nil {
			applicationFatal(err)
		}
	}
	options.Interceptors = append(options.Interceptors, &observabilityInterceptor{})
	sdkWorker := factory(c.SDK(), config.TaskQueue, options)
	return &Worker{
		name:            config.Name,
		sdk:             sdkWorker,
		runtime:         sdkWorker,
		shutdownTimeout: shutdownTimeout,
		fatal:           fatal,
		startDone:       make(chan struct{}),
		stopDone:        make(chan struct{}),
		state:           workerNew,
	}, nil
}

// SDK exposes workflow/activity registration and other native Temporal APIs.
func (w *Worker) SDK() worker.Worker { return w.sdk }

// Name identifies this worker to the common lifecycle runner.
func (w *Worker) Name() string { return w.name }

// Run starts polling and returns after cancellation, shutdown, or a fatal SDK error.
func (w *Worker) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	w.mu.Lock()
	if w.state != workerNew {
		w.mu.Unlock()
		return ErrInvalidWorkerState
	}
	w.state = workerStarting
	w.mu.Unlock()

	if err := w.runtime.Start(); err != nil {
		startErr := fmt.Errorf("%w: %w", ErrWorkerStartFailed, err)
		w.mu.Lock()
		w.state = workerStopped
		close(w.startDone)
		close(w.stopDone)
		w.mu.Unlock()
		return startErr
	}

	w.mu.Lock()
	w.state = workerRunning
	close(w.startDone)
	w.mu.Unlock()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-w.fatal:
		runErr = fmt.Errorf("worker failed: %w", runErr)
	case <-w.stopDone:
		return nil
	}

	shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.shutdownTimeout)
	defer cancel()
	return errors.Join(runErr, w.Shutdown(shutdownContext))
}

// Shutdown stops native polling once. If the caller times out, a later call waits on the same stop.
func (w *Worker) Shutdown(ctx context.Context) error {
	for {
		w.mu.Lock()
		switch w.state {
		case workerNew:
			w.state = workerStopped
			close(w.startDone)
			close(w.stopDone)
			w.mu.Unlock()
			return nil
		case workerStarting:
			started := w.startDone
			w.mu.Unlock()
			select {
			case <-started:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		case workerRunning:
			w.state = workerStopping
			go w.stop()
			done := w.stopDone
			w.mu.Unlock()
			return waitForWorkerStop(ctx, done)
		case workerStopping:
			done := w.stopDone
			w.mu.Unlock()
			return waitForWorkerStop(ctx, done)
		case workerStopped:
			w.mu.Unlock()
			return nil
		default:
			w.mu.Unlock()
			return ErrInvalidWorkerState
		}
	}
}

func (w *Worker) stop() {
	w.runtime.Stop()
	w.mu.Lock()
	w.state = workerStopped
	close(w.stopDone)
	w.mu.Unlock()
}

func waitForWorkerStop(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ lifecycle.Component = (*Worker)(nil)
