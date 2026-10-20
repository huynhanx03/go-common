package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strconv"
	"sync"

	"github.com/huynhanx03/go-common/pkg/logger"
)

const maxPanicStackBytes = 32 << 10

type runnerState uint8

const (
	stateNew runnerState = iota
	stateRunning
	stateStopping
	stateStopped
)

type registration struct {
	name      string
	component Component
	options   ComponentOptions
}

type runResult struct {
	registration registration
	err          error
}

// Runner owns component registration, cancellation, and bounded reverse-order
// shutdown. It is safe for concurrent Run and Shutdown calls.
type Runner struct {
	mu            sync.Mutex
	options       Options
	state         runnerState
	registrations []registration
	runCancel     context.CancelFunc
	shutdownDone  chan struct{}
	shutdownErr   error
}

// New constructs a lifecycle runner.
func New(options Options) (*Runner, error) {
	options = options.withDefaults()
	if options.ShutdownTimeout <= 0 ||
		options.ShutdownTimeout > maximumShutdownTimeout ||
		options.MaxComponents <= 0 ||
		options.MaxComponents > maximumComponents {
		return nil, ErrInvalidOptions
	}
	return &Runner{
		options:      options,
		state:        stateNew,
		shutdownDone: make(chan struct{}),
	}, nil
}

// Add registers a component. Registration is only allowed before Run or
// Shutdown starts.
func (runner *Runner) Add(component Component, options ComponentOptions) error {
	if runner == nil {
		return ErrInvalidState
	}
	if isNilComponent(component) {
		return ErrInvalidComponent
	}
	if options.ShutdownTimeout < 0 || options.ShutdownTimeout > maximumShutdownTimeout {
		return ErrInvalidOptions
	}

	name, err := safeComponentName(component)
	if err != nil || !validComponentName(name) {
		return errors.Join(ErrInvalidComponent, err)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.state != stateNew {
		return ErrInvalidState
	}
	if len(runner.registrations) >= runner.options.MaxComponents {
		return fmt.Errorf("%w: maximum %d", ErrInvalidOptions, runner.options.MaxComponents)
	}
	for _, existing := range runner.registrations {
		if existing.name == name {
			return fmt.Errorf("%w: %s", ErrDuplicateComponent, name)
		}
	}
	if options.ShutdownTimeout == 0 {
		options.ShutdownTimeout = runner.options.ShutdownTimeout
	}
	runner.registrations = append(runner.registrations, registration{
		name:      name,
		component: component,
		options:   options,
	})
	return nil
}

// Run starts every registered component concurrently and blocks until the
// caller cancels the context or a required component exits unexpectedly. It
// performs bounded shutdown before returning.
func (runner *Runner) Run(ctx context.Context) error {
	if runner == nil {
		return ErrInvalidState
	}
	ctx = nonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}

	runner.mu.Lock()
	if runner.state != stateNew {
		runner.mu.Unlock()
		return ErrInvalidState
	}
	runCtx, cancel := context.WithCancel(ctx)
	runner.runCancel = cancel
	runner.state = stateRunning
	registrations := append([]registration(nil), runner.registrations...)
	runner.mu.Unlock()
	runLogContext := logger.WithFields(
		runCtx,
		logger.String("component_count", strconv.Itoa(len(registrations))),
	)
	logger.FromContext(runLogContext).Info("lifecycle runner started")

	results := make(chan runResult, len(registrations))
	active := make(map[string]struct{}, len(registrations))
	for _, item := range registrations {
		item := item
		active[item.name] = struct{}{}
		go func() {
			componentContext := logger.WithFields(
				runCtx,
				logger.String("component_name", item.name),
				logger.String("required", strconv.FormatBool(item.options.Required)),
			)
			logger.FromContext(componentContext).Debug("lifecycle component starting")
			err := safeRun(item, runCtx)
			componentLogger := logger.FromContext(componentContext)
			switch {
			case runCtx.Err() != nil:
				componentLogger.Info("lifecycle component stopped")
			case err != nil && item.options.Required:
				componentLogger.Error("lifecycle required component stopped unexpectedly")
			case err != nil:
				componentLogger.Warn("lifecycle optional component stopped")
			default:
				componentLogger.Info("lifecycle component stopped")
			}
			results <- runResult{registration: item, err: err}
		}()
	}

	var primary error

waitForStop:
	for {
		select {
		case <-runCtx.Done():
			break waitForStop
		case result := <-results:
			delete(active, result.registration.name)
			if runCtx.Err() != nil || !result.registration.options.Required {
				continue
			}
			if result.err == nil {
				result.err = ErrUnexpectedExit
			}
			primary = &ComponentError{
				Component: result.registration.name,
				Operation: "run",
				Err:       result.err,
			}
			cancel()
			break waitForStop
		}
	}

	// Shutdown must start before waiting for every Run method. Some valid
	// components use Shutdown to release resources that their Run loop is
	// waiting on. The outer deadline also bounds components that violate the
	// cancellation contract, which Go cannot forcibly terminate.
	cleanupContext, cleanupCancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		runner.options.ShutdownTimeout,
	)
	defer cleanupCancel()
	shutdownErr := runner.Shutdown(cleanupContext)
	drainErr := waitForRunDrain(cleanupContext, results, active, registrations)
	logger.FromContext(runLogContext).Info("lifecycle runner stopped")
	return errors.Join(primary, shutdownErr, drainErr)
}

// Shutdown cancels component Run contexts and invokes Shutdown once in reverse
// registration order. Concurrent and repeated callers share the same result.
func (runner *Runner) Shutdown(ctx context.Context) error {
	if runner == nil {
		return ErrInvalidState
	}
	ctx = nonNilContext(ctx)

	runner.mu.Lock()
	switch runner.state {
	case stateStopped:
		err := runner.shutdownErr
		runner.mu.Unlock()
		return err
	case stateStopping:
		done := runner.shutdownDone
		runner.mu.Unlock()
		select {
		case <-done:
			runner.mu.Lock()
			err := runner.shutdownErr
			runner.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	case stateNew, stateRunning:
		runner.state = stateStopping
	default:
		runner.mu.Unlock()
		return ErrInvalidState
	}
	if runner.runCancel != nil {
		runner.runCancel()
	}
	registrations := append([]registration(nil), runner.registrations...)
	runner.mu.Unlock()

	shutdownErr := runner.shutdown(ctx, registrations)

	runner.mu.Lock()
	runner.shutdownErr = shutdownErr
	runner.state = stateStopped
	close(runner.shutdownDone)
	runner.mu.Unlock()
	return shutdownErr
}

func (runner *Runner) shutdown(ctx context.Context, registrations []registration) error {
	totalCtx, cancel := context.WithTimeout(ctx, runner.options.ShutdownTimeout)
	defer cancel()

	var failures []error
	for index := len(registrations) - 1; index >= 0; index-- {
		item := registrations[index]
		if err := totalCtx.Err(); err != nil {
			failures = append(failures, err)
			break
		}

		componentCtx, componentCancel := context.WithTimeout(totalCtx, item.options.ShutdownTimeout)
		componentCtx = logger.WithFields(
			componentCtx,
			logger.String("component_name", item.name),
		)
		logger.FromContext(componentCtx).Debug("lifecycle component shutdown starting")
		err := boundedShutdown(componentCtx, item)
		componentCancel()
		if err != nil {
			logger.FromContext(componentCtx).Error("lifecycle component shutdown failed")
			failures = append(failures, &ComponentError{
				Component: item.name,
				Operation: "shutdown",
				Err:       err,
			})
			continue
		}
		logger.FromContext(componentCtx).Info("lifecycle component shutdown complete")
	}
	return errors.Join(failures...)
}

func safeRun(item registration, ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicError(recovered)
		}
	}()
	return item.component.Run(ctx)
}

func boundedShutdown(ctx context.Context, item registration) error {
	result := make(chan error, 1)
	go func() {
		result <- safeShutdown(item, ctx)
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func safeShutdown(item registration, ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicError(recovered)
		}
	}()
	return item.component.Shutdown(ctx)
}

func safeComponentName(component Component) (name string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicError(recovered)
		}
	}()
	return component.Name(), nil
}

func panicError(value any) error {
	stack := debug.Stack()
	if len(stack) > maxPanicStackBytes {
		stack = stack[:maxPanicStackBytes]
	}
	return fmt.Errorf("%w: panic_type=%T\n%s", ErrComponentPanic, value, stack)
}

func waitForRunDrain(
	ctx context.Context,
	results <-chan runResult,
	active map[string]struct{},
	registrations []registration,
) error {
	for len(active) > 0 {
		select {
		case result := <-results:
			delete(active, result.registration.name)
		case <-ctx.Done():
			// Prefer results that were already published at the deadline over
			// reporting those components as undrained.
			for {
				select {
				case result := <-results:
					delete(active, result.registration.name)
				default:
					return runDrainError(registrations, active, ctx.Err())
				}
			}
		}
	}
	return nil
}

func runDrainError(registrations []registration, active map[string]struct{}, cause error) error {
	var failures []error
	for _, item := range registrations {
		if _, exists := active[item.name]; !exists {
			continue
		}
		failures = append(failures, &ComponentError{
			Component: item.name,
			Operation: "drain",
			Err:       cause,
		})
	}
	return errors.Join(failures...)
}

func validComponentName(name string) bool {
	if len(name) == 0 || len(name) > maxComponentNameBytes {
		return false
	}
	for index := 0; index < len(name); index++ {
		char := name[index]
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '.' || char == ':' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func isNilComponent(component Component) bool {
	if component == nil {
		return true
	}
	value := reflect.ValueOf(component)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
