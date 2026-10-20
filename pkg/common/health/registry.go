package health

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxConcurrent    = 8
	defaultMaxChecks        = 128
	maximumConcurrentChecks = 256
	maximumChecks           = 4096
	maximumCheckTimeout     = 24 * time.Hour
	maxCheckNameBytes       = 64
	maxErrorCodeBytes       = 64
)

var (
	ErrInvalidOptions = errors.New("health: invalid options")
	ErrInvalidCheck   = errors.New("health: invalid check")
	ErrDuplicateCheck = errors.New("health: duplicate check")
)

type checkEntry struct {
	name     string
	checker  Checker
	options  CheckOptions
	inFlight atomic.Bool
}

// Registry owns a bounded set of liveness and readiness checkers.
type Registry struct {
	mu            sync.RWMutex
	entries       []*checkEntry
	names         map[string]struct{}
	maxConcurrent int
	maxChecks     int
	now           func() time.Time
}

// New constructs an empty health registry.
func New(options Options) (*Registry, error) {
	if options.MaxConcurrent < 0 ||
		options.MaxConcurrent > maximumConcurrentChecks ||
		options.MaxChecks < 0 ||
		options.MaxChecks > maximumChecks {
		return nil, ErrInvalidOptions
	}
	if options.MaxConcurrent == 0 {
		options.MaxConcurrent = defaultMaxConcurrent
	}
	if options.MaxChecks == 0 {
		options.MaxChecks = defaultMaxChecks
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Registry{
		names:         make(map[string]struct{}),
		maxConcurrent: options.MaxConcurrent,
		maxChecks:     options.MaxChecks,
		now:           options.Now,
	}, nil
}

// Register adds a bounded checker. Names are unique across both probe types.
func (registry *Registry) Register(name string, checker Checker, options CheckOptions) error {
	if registry == nil || !validName(name) || isNilChecker(checker) || !validCheckOptions(options) {
		return ErrInvalidCheck
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.names[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateCheck, name)
	}
	if len(registry.entries) >= registry.maxChecks {
		return fmt.Errorf("%w: maximum %d", ErrInvalidCheck, registry.maxChecks)
	}
	registry.entries = append(registry.entries, &checkEntry{
		name:    name,
		checker: checker,
		options: options,
	})
	registry.names[name] = struct{}{}
	return nil
}

// CheckReadiness checks whether the process should receive new traffic.
func (registry *Registry) CheckReadiness(ctx context.Context) Report {
	return registry.check(ctx, ProbeReadiness)
}

// CheckLiveness checks whether the process and essential loops can continue.
func (registry *Registry) CheckLiveness(ctx context.Context) Report {
	return registry.check(ctx, ProbeLiveness)
}

func (registry *Registry) check(ctx context.Context, probe Probe) Report {
	if registry == nil {
		return unavailableReport()
	}
	ctx = nonNilContext(ctx)

	registry.mu.RLock()
	entries := make([]*checkEntry, 0, len(registry.entries))
	for _, entry := range registry.entries {
		if entry.options.Probes&probe != 0 {
			entries = append(entries, entry)
		}
	}
	registry.mu.RUnlock()

	if len(entries) == 0 {
		return Report{Healthy: true, Statuses: []Status{}}
	}

	semaphore := make(chan struct{}, registry.maxConcurrent)
	results := make(chan Status, len(entries))
	var wait sync.WaitGroup
	for _, entry := range entries {
		entry := entry
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
				results <- registry.execute(ctx, entry)
			case <-ctx.Done():
				results <- Status{
					Name:      entry.name,
					Healthy:   false,
					Critical:  entry.options.Critical,
					ErrorCode: entry.options.ErrorCode,
				}
			}
		}()
	}
	wait.Wait()
	close(results)

	report := Report{
		Healthy:  true,
		Statuses: make([]Status, 0, len(entries)),
	}
	for status := range results {
		report.Statuses = append(report.Statuses, status)
		if !status.Healthy && status.Critical {
			report.Healthy = false
		}
	}
	sort.Slice(report.Statuses, func(left, right int) bool {
		return report.Statuses[left].Name < report.Statuses[right].Name
	})
	return report
}

func (registry *Registry) execute(parent context.Context, entry *checkEntry) (status Status) {
	start := registry.now()
	status = Status{
		Name:     entry.name,
		Healthy:  false,
		Critical: entry.options.Critical,
	}
	defer func() {
		status.Duration = registry.now().Sub(start)
		if status.Duration < 0 {
			status.Duration = 0
		}
	}()

	if !entry.inFlight.CompareAndSwap(false, true) {
		status.ErrorCode = entry.options.ErrorCode
		return status
	}

	ctx, cancel := context.WithTimeout(parent, entry.options.Timeout)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		defer entry.inFlight.Store(false)
		result <- safeCheck(entry.checker, ctx)
	}()

	var err error
	select {
	case err = <-result:
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err == nil {
		status.Healthy = true
		return status
	}
	status.ErrorCode = entry.options.ErrorCode
	return status
}

func safeCheck(checker Checker, ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("health: checker panic")
		}
	}()
	return checker.Check(ctx)
}

func unavailableReport() Report {
	return Report{
		Healthy: false,
		Statuses: []Status{{
			Name:      "registry",
			Healthy:   false,
			Critical:  true,
			ErrorCode: "health_registry_unavailable",
		}},
	}
}

func validCheckOptions(options CheckOptions) bool {
	const allowed = ProbeLiveness | ProbeReadiness
	return options.Probes != 0 &&
		options.Probes&^allowed == 0 &&
		options.Timeout > 0 &&
		options.Timeout <= maximumCheckTimeout &&
		validErrorCode(options.ErrorCode)
}

func validName(name string) bool {
	if len(name) == 0 || len(name) > maxCheckNameBytes {
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

func validErrorCode(code string) bool {
	if len(code) == 0 || len(code) > maxErrorCodeBytes {
		return false
	}
	for index := 0; index < len(code); index++ {
		char := code[index]
		if (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '.' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func isNilChecker(checker Checker) bool {
	if checker == nil {
		return true
	}
	value := reflect.ValueOf(checker)
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
