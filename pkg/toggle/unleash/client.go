// Package unleash evaluates toggle definitions using the official Unleash SDK.
package unleash

import (
	"context"
	"net/http"
	"sync"

	sdk "github.com/Unleash/unleash-go-sdk/v6"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

type client struct {
	mu     sync.RWMutex
	engine *sdk.Client
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
		l.onError(core.SourceError(err))
	}
}
func (l *listener) OnWarning(err error) { l.OnError(err) }

// New loads one complete snapshot before returning, then lets the SDK refresh
// snapshots in the background. Evaluations stay local to the SDK cache.
func New(ctx context.Context, config settings.Toggle, source core.Source, onError func(error)) (core.Client, error) {
	if ctx == nil {
		return nil, core.ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolved, err := configFromSettings(config, source, onError)
	if err != nil {
		return nil, err
	}
	return newClient(ctx, resolved)
}

func newClient(ctx context.Context, config clientConfig) (core.Client, error) {
	if ctx == nil {
		return nil, core.ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}

	snapshot, err := loadInitialSnapshot(ctx, config)
	if err != nil {
		return nil, core.SourceError(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lifetime, cancel := context.WithCancel(context.Background())
	transport := &sourceTransport{
		source: config.source, scope: config.scope, timeout: config.sourceTimeout,
		lifetime: lifetime, accepted: snapshot, first: true,
	}
	events := &listener{ready: make(chan struct{}), onError: config.onError}
	engine, err := sdk.NewClient(
		sdk.WithAppName(config.appName), sdk.WithEnvironment(config.scope.Environment),
		sdk.WithInstanceId(sdkInstanceID), sdk.WithUrl(sdkBaseURL),
		sdk.WithRefreshInterval(config.refreshInterval), sdk.WithDisableMetrics(true),
		sdk.WithStorage(&bootstrapStorage{initial: mapSnapshot(snapshot)}),
		sdk.WithHttpClient(&http.Client{Transport: transport}), sdk.WithListener(events),
	)
	if err != nil {
		cancel()
		return nil, core.SourceError(err)
	}

	c := &client{engine: engine, cancel: cancel, done: make(chan struct{})}
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

func (c *client) startClose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
	go func() {
		_ = c.engine.Close()
		close(c.done)
	}()
}

// Close stops refresh work and waits for the SDK to finish. A caller deadline
// only stops that caller's wait; another Close can wait for the same cleanup.
func (c *client) Close(ctx context.Context) error {
	if ctx == nil {
		return core.ErrInvalidContext
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
