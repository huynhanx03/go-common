// Package unleash evaluates toggle definitions using the official Unleash SDK.
package unleash

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"time"

	sdk "github.com/Unleash/unleash-go-sdk/v6"
	"github.com/huynhanx03/go-common/pkg/toggle"
)

type Config struct {
	Scope           toggle.Scope
	AppName         string
	Source          toggle.Source
	RefreshInterval time.Duration
	SourceTimeout   time.Duration
	// OnError must be quick, nonblocking, nonpanicking and must not recursively Close.
	OnError func(error)
}

type client struct {
	mu     sync.RWMutex
	sdk    *sdk.Client
	closed bool
	cancel context.CancelFunc
	done   chan struct{}
}

type listener struct {
	sdk.NoopListener
	ready   chan struct{}
	onError func(error)
}

func (l *listener) OnReady() { close(l.ready) }
func (l *listener) OnError(err error) {
	if l.onError != nil {
		l.onError(sourceError{err})
	}
}
func (l *listener) OnWarning(err error) { l.OnError(err) }

// New synchronously admits the first complete snapshot, then starts the SDK's
// sole polling loop. Evaluation never accesses Source or a network transport.
func New(ctx context.Context, config Config) (toggle.Client, error) {
	if ctx == nil {
		return nil, toggle.ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := toggle.ValidateScope(config.Scope); err != nil {
		return nil, err
	}
	if err := validateName(config.AppName, 100, toggle.ErrInvalidConfig); err != nil {
		return nil, err
	}
	if config.Source == nil || nilValue(config.Source) || config.RefreshInterval < 0 || config.SourceTimeout < 0 {
		return nil, toggle.ErrInvalidConfig
	}
	if config.RefreshInterval == 0 {
		config.RefreshInterval = 15 * time.Second
	}
	if config.SourceTimeout == 0 {
		config.SourceTimeout = 5 * time.Second
	}
	loadCtx, loadCancel := context.WithTimeout(ctx, config.SourceTimeout)
	snapshot, err := config.Source.Load(loadCtx, config.Scope)
	if err == nil {
		err = loadCtx.Err()
	}
	loadCancel()
	if err != nil {
		return nil, sourceError{err}
	}
	snapshot, err = toggle.NormalizeSnapshot(config.Scope, snapshot)
	if err != nil {
		return nil, sourceError{err}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	transport := &sourceTransport{source: config.Source, scope: config.Scope, timeout: config.SourceTimeout, lifetime: lifetime, accepted: snapshot, first: true}
	events := &listener{ready: make(chan struct{}), onError: config.OnError}
	engine, err := sdk.NewClient(
		sdk.WithAppName(config.AppName), sdk.WithEnvironment(config.Scope.Environment),
		sdk.WithInstanceId("toggle-local"), sdk.WithUrl("http://toggle.invalid/api/"),
		sdk.WithRefreshInterval(config.RefreshInterval), sdk.WithDisableMetrics(true),
		sdk.WithStorage(&memoryStorage{initial: mapSnapshot(snapshot)}),
		sdk.WithHttpClient(&http.Client{Transport: transport}), sdk.WithListener(events),
	)
	if err != nil {
		cancel()
		return nil, sourceError{err}
	}
	c := &client{sdk: engine, cancel: cancel, done: make(chan struct{})}
	select {
	case <-events.ready:
		if err := ctx.Err(); err != nil {
			c.startClose()
			return nil, err
		}
		return c, nil
	case <-ctx.Done():
		c.startClose()
		return nil, ctx.Err()
	}
}

func nilValue(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func validateName(value string, limit int, category error) error {
	if len(value) > limit {
		return evaluationError{category, toggle.ErrLimit}
	}
	if value == "" {
		return category
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-') {
			return category
		}
	}
	return nil
}

type evaluationError struct{ category, cause error }

func (e evaluationError) Error() string   { return e.category.Error() }
func (e evaluationError) Unwrap() []error { return []error{e.category, e.cause} }

func validateEvaluation(ctx context.Context, key string, evaluation toggle.EvaluationContext) error {
	if ctx == nil {
		return toggle.ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateName(key, 100, toggle.ErrInvalidContext); err != nil {
		return err
	}
	if len(evaluation.UserID) > 4096 || len(evaluation.SessionID) > 4096 || len(evaluation.RemoteAddress) > 4096 || len(evaluation.Properties) > 100 {
		return evaluationError{toggle.ErrInvalidContext, toggle.ErrLimit}
	}
	for name, value := range evaluation.Properties {
		if err := validateName(name, 1024, toggle.ErrInvalidContext); err != nil {
			return err
		}
		switch name {
		case "userId", "sessionId", "remoteAddress", "appName", "environment":
			return toggle.ErrInvalidContext
		}
		if len(value) > 4096 {
			return evaluationError{toggle.ErrInvalidContext, toggle.ErrLimit}
		}
	}
	return nil
}

func (c *client) IsEnabled(ctx context.Context, key string, evaluation toggle.EvaluationContext, fallback bool) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return fallback, toggle.ErrClosed
	}
	if err := validateEvaluation(ctx, key, evaluation); err != nil {
		return fallback, err
	}
	return c.sdk.IsEnabled(key, sdk.FeatureOptions{Ctx: mapContext(evaluation), Fallback: &fallback}), nil
}
func (c *client) GetVariant(ctx context.Context, key string, evaluation toggle.EvaluationContext) (toggle.VariantResult, error) {
	disabled := toggle.VariantResult{Name: "disabled"}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return disabled, toggle.ErrClosed
	}
	if err := validateEvaluation(ctx, key, evaluation); err != nil {
		return disabled, err
	}
	variant := c.sdk.GetVariant(key, sdk.VariantOptions{Ctx: mapContext(evaluation)})
	result := toggle.VariantResult{Name: variant.Name, Enabled: variant.Enabled, FeatureEnabled: variant.FeatureEnabled}
	if variant.Payload.Type != "" {
		result.Payload = &toggle.Payload{Type: variant.Payload.Type, Value: variant.Payload.Value}
	}
	return result, nil
}

func (c *client) startClose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
	go func() { _ = c.sdk.Close(); close(c.done) }()
}

// Close fences evaluations immediately. A caller deadline does not interrupt
// cleanup; another Close can wait for it. Sources must honor cancellation.
func (c *client) Close(ctx context.Context) error {
	if ctx == nil {
		return toggle.ErrInvalidContext
	}
	c.startClose()
	select {
	case <-c.done:
		return nil
	default:
	}
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
