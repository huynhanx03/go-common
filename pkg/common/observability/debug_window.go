package observability

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
)

var (
	ErrInvalidDebugWindow  = errors.New("observability: invalid debug window")
	ErrDebugWindowConflict = errors.New("observability: debug window idempotency conflict")
	ErrDebugLevelMismatch  = errors.New("observability: debug level does not match active window")
	ErrDebugLevelControl   = errors.New("observability: debug level control failed")
)

const (
	maxDebugComponentBytes = 64
	maxDebugReasonBytes    = 1024
	maxDebugKeyBytes       = 128
	maxDebugCompletions    = 1024
	maxDebugDuration       = 24 * time.Hour
	debugRevertRetry       = time.Second
)

// LevelControl is the vendor-neutral boundary implemented by a structured
// logger with a runtime-adjustable level.
type LevelControl interface {
	CurrentLevel() string
	SetLevel(string) error
}

// DebugRequest is one bounded temporary level override. Key is an
// idempotency key supplied by the caller; authorization, reauthentication,
// persistence, and audit remain application responsibilities.
type DebugRequest struct {
	Key       string
	Component string
	Level     string
	Duration  time.Duration
	Reason    string
}

// DebugLease is the safe projection returned to trusted control-plane code.
type DebugLease struct {
	Key       string
	Component string
	Level     string
	StartedAt time.Time
	ExpiresAt time.Time
}

// DebugWindow applies exactly one process-local, time-bounded level override.
// A newer accepted lease fences the previous timer. Shutdown closes the
// controller only after the configured baseline level has been restored.
type DebugWindow struct {
	control  LevelControl
	baseline string
	maximum  time.Duration
	now      func() time.Time

	mu          sync.Mutex
	timer       *time.Timer
	generation  uint64
	active      *DebugLease
	completions map[string]debugCompletion
	order       []string
	closed      bool
}

type debugCompletion struct {
	fingerprint [sha256.Size]byte
	lease       DebugLease
}

// Name implements the shared lifecycle component shape without importing the
// lifecycle package.
func (*DebugWindow) Name() string { return "debug-window" }

// Run owns the automatic-revert timer until its parent lifecycle is canceled.
func (window *DebugWindow) Run(ctx context.Context) error {
	if window == nil || ctx == nil {
		return ErrInvalidDebugWindow
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	<-ctx.Done()
	return window.restoreAndClose()
}

// NewDebugWindow constructs a controller. baseline is applied on expiry and
// shutdown; maximum is the hard upper bound accepted from callers.
func NewDebugWindow(
	control LevelControl,
	baseline string,
	maximum time.Duration,
	now func() time.Time,
) (*DebugWindow, error) {
	if nilLevelControl(control) ||
		!validDebugToken(baseline, maxDebugComponentBytes) ||
		maximum <= 0 ||
		maximum > maxDebugDuration {
		return nil, ErrInvalidDebugWindow
	}
	if now == nil {
		now = time.Now
	}
	return &DebugWindow{
		control: control, baseline: baseline, maximum: maximum, now: now,
		completions: make(map[string]debugCompletion),
		order:       make([]string, 0, maxDebugCompletions),
	}, nil
}

// Activate applies or idempotently replays a temporary override.
func (window *DebugWindow) Activate(ctx context.Context, request DebugRequest) (DebugLease, error) {
	if window == nil || ctx == nil {
		return DebugLease{}, ErrInvalidDebugWindow
	}
	if err := ctx.Err(); err != nil {
		return DebugLease{}, err
	}
	if err := validateDebugRequest(request, window.maximum); err != nil {
		return DebugLease{}, err
	}
	fingerprint := debugFingerprint(request)

	window.mu.Lock()
	defer window.mu.Unlock()
	if window.closed {
		return DebugLease{}, ErrInvalidDebugWindow
	}
	if previous, exists := window.completions[request.Key]; exists {
		if previous.fingerprint != fingerprint {
			return DebugLease{}, ErrDebugWindowConflict
		}
		return previous.lease, nil
	}
	now := window.now().UTC()
	lease := DebugLease{
		Key: request.Key, Component: request.Component, Level: request.Level,
		StartedAt: now, ExpiresAt: now.Add(request.Duration),
	}
	if err := setLevel(window.control, request.Level); err != nil {
		applyFailure := fmt.Errorf("%w: apply level", ErrDebugLevelControl)
		restoreFailure := setLevel(window.control, window.baseline)
		if restoreFailure == nil {
			window.clearLeaseLocked()
			return DebugLease{}, applyFailure
		}
		current, currentFailure := currentLevel(window.control)
		if currentFailure == nil && current == window.baseline {
			window.clearLeaseLocked()
			return DebugLease{}, errors.Join(
				applyFailure,
				fmt.Errorf("%w: restore baseline", ErrDebugLevelControl),
			)
		}
		// SetLevel may fail after partially applying the requested level. If
		// baseline restoration is also inconclusive, expose a fenced lease and
		// keep retrying instead of reporting an inactive safe state.
		retryDelay := debugRevertRetry
		if request.Duration < retryDelay {
			retryDelay = request.Duration
		}
		window.installLeaseLocked(lease, retryDelay)
		return DebugLease{}, errors.Join(
			applyFailure,
			fmt.Errorf("%w: restore baseline", ErrDebugLevelControl),
		)
	}
	window.installLeaseLocked(lease, request.Duration)
	window.remember(request.Key, debugCompletion{
		fingerprint: fingerprint,
		lease:       lease,
	})
	return lease, nil
}

func (window *DebugWindow) installLeaseLocked(
	lease DebugLease,
	revertAfter time.Duration,
) {
	window.generation++
	generation := window.generation
	if window.timer != nil {
		window.timer.Stop()
	}
	window.active = &lease
	window.timer = time.AfterFunc(revertAfter, func() {
		window.expire(generation)
	})
}

func (window *DebugWindow) clearLeaseLocked() {
	window.generation++
	if window.timer != nil {
		window.timer.Stop()
		window.timer = nil
	}
	window.active = nil
}

// Revoke restores the baseline only when key owns the active lease.
func (window *DebugWindow) Revoke(ctx context.Context, key string) error {
	if window == nil || ctx == nil || !validDebugToken(key, maxDebugKeyBytes) {
		return ErrInvalidDebugWindow
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.closed {
		return ErrInvalidDebugWindow
	}
	if window.active == nil || window.active.Key != key {
		return nil
	}
	if err := setLevel(window.control, window.baseline); err != nil {
		return fmt.Errorf("%w: restore baseline", ErrDebugLevelControl)
	}
	window.generation++
	if window.timer != nil {
		window.timer.Stop()
		window.timer = nil
	}
	window.active = nil
	return nil
}

// Active returns a copy of the effective current lease. It also closes the
// small scheduling gap where a lease is already expired but its timer callback
// has not run yet. A failed baseline restore remains visible as active so the
// control plane never reports a safe state while debug logging is still on.
func (window *DebugWindow) Active() (DebugLease, bool) {
	if window == nil {
		return DebugLease{}, false
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.active == nil || window.closed {
		return DebugLease{}, false
	}
	if !window.active.ExpiresAt.After(window.now().UTC()) {
		if err := window.restoreExpiredLocked(); err == nil {
			return DebugLease{}, false
		}
	}
	return *window.active, true
}

// Check reports whether the controlled logger matches the active lease (or
// baseline) and whether an expired lease has reverted. It returns only stable
// classifications, never operator-supplied reason/component text.
func (window *DebugWindow) Check(ctx context.Context) error {
	if window == nil || ctx == nil {
		return ErrInvalidDebugWindow
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.closed {
		return ErrInvalidDebugWindow
	}
	current, err := currentLevel(window.control)
	if err != nil {
		return ErrDebugLevelControl
	}
	if window.active == nil {
		if current != window.baseline {
			return ErrDebugLevelMismatch
		}
		return nil
	}
	if current != window.active.Level || !window.active.ExpiresAt.After(window.now().UTC()) {
		return ErrDebugLevelMismatch
	}
	return nil
}

// Shutdown cancels timers and restores the baseline. It is idempotent.
func (window *DebugWindow) Shutdown(ctx context.Context) error {
	if window == nil || ctx == nil {
		return ErrInvalidDebugWindow
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return window.restoreAndClose()
}

func (window *DebugWindow) restoreAndClose() error {
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.closed {
		return nil
	}
	if err := setLevel(window.control, window.baseline); err != nil {
		return fmt.Errorf("%w: restore baseline", ErrDebugLevelControl)
	}
	// Publish the terminal state only after the safety action succeeds. On a
	// failed restore the active lease and timer remain visible, allowing health
	// checks, the expiry retry, or a later Shutdown call to recover.
	window.closed = true
	window.generation++
	if window.timer != nil {
		window.timer.Stop()
		window.timer = nil
	}
	window.active = nil
	return nil
}

func (window *DebugWindow) expire(generation uint64) {
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.closed || generation != window.generation || window.active == nil {
		return
	}
	if err := window.restoreExpiredLocked(); err != nil {
		// Keep the lease visible: control-plane health can then report that a
		// safety-critical automatic revert did not complete.
		window.timer = time.AfterFunc(debugRevertRetry, func() {
			window.expire(generation)
		})
	}
}

func (window *DebugWindow) restoreExpiredLocked() error {
	if err := setLevel(window.control, window.baseline); err != nil {
		return fmt.Errorf("%w: restore baseline", ErrDebugLevelControl)
	}
	window.active = nil
	if window.timer != nil {
		window.timer.Stop()
	}
	window.timer = nil
	return nil
}

func (window *DebugWindow) remember(key string, completion debugCompletion) {
	if len(window.completions) >= maxDebugCompletions {
		evicted := window.order[0]
		window.order = window.order[1:]
		delete(window.completions, evicted)
	}
	window.completions[key] = completion
	window.order = append(window.order, key)
}

func validateDebugRequest(request DebugRequest, maximum time.Duration) error {
	if !validDebugToken(request.Key, maxDebugKeyBytes) ||
		!validDebugToken(request.Component, maxDebugComponentBytes) ||
		!validDebugToken(request.Level, maxDebugComponentBytes) ||
		strings.TrimSpace(request.Reason) != request.Reason ||
		request.Reason == "" ||
		len(request.Reason) > maxDebugReasonBytes ||
		request.Duration <= 0 || request.Duration > maximum {
		return ErrInvalidDebugWindow
	}
	for _, char := range request.Reason {
		if unicode.IsControl(char) {
			return ErrInvalidDebugWindow
		}
	}
	return nil
}

func validDebugToken(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '.' || char == '_' || char == '-' || char == ':' {
			continue
		}
		return false
	}
	return true
}

func nilLevelControl(control LevelControl) bool {
	if control == nil {
		return true
	}
	value := reflect.ValueOf(control)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func setLevel(control LevelControl, level string) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrDebugLevelControl
		}
	}()
	return control.SetLevel(level)
}

func currentLevel(control LevelControl) (level string, err error) {
	defer func() {
		if recover() != nil {
			level = ""
			err = ErrDebugLevelControl
		}
	}()
	return control.CurrentLevel(), nil
}

func debugFingerprint(request DebugRequest) [sha256.Size]byte {
	return sha256.Sum256([]byte(fmt.Sprintf(
		"%d:%s%d:%s%d:%s%d:%s%d",
		len(request.Component), request.Component,
		len(request.Level), request.Level,
		len(request.Reason), request.Reason,
		len(request.Key), request.Key,
		request.Duration,
	)))
}
