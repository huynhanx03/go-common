package i18n

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/settings"
)

// ClientConfig borrows the application's source and cache.
// L1 stores compiled bundles; L2, when configured, stores serializable data.
type ClientConfig struct {
	Source Source
	Cache  *cache.Cache
}

type Client struct {
	domain        string
	source        Source
	cache         *cache.Cache
	ttl           time.Duration
	sourceTimeout time.Duration
	live          context.Context
	cancel        context.CancelFunc
	closed        atomic.Bool
	epoch         atomic.Uint64
	index         atomic.Pointer[localeIndex]
	stateMu       sync.RWMutex
	closeOnce     sync.Once
}

// New validates dependencies without loading messages.
// The application retains ownership of the injected cache and source.
func New(ctx context.Context, config settings.I18n, dependencies ClientConfig) (*Client, error) {
	if err := contextOK(ctx); err != nil {
		return nil, err
	}
	if ValidateDomain(config.Domain) != nil || isNil(dependencies.Source) || dependencies.Cache == nil || !dependencies.Cache.HasL1() {
		return nil, ErrInvalidConfig
	}
	ttl, err := secondsOrDefault(config.CacheTTLSeconds, DefaultCacheTTLSeconds)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	timeout, err := secondsOrDefault(config.SourceTimeoutSeconds, DefaultSourceTimeoutSeconds)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	live, cancel := context.WithCancel(context.Background())
	client := &Client{domain: config.Domain, source: dependencies.Source, cache: dependencies.Cache, ttl: ttl, sourceTimeout: timeout, live: live, cancel: cancel}
	return client, nil
}

func secondsOrDefault(seconds, defaultSeconds int) (time.Duration, error) {
	if seconds == 0 {
		seconds = defaultSeconds
	}
	if seconds < 1 || int64(seconds) > math.MaxInt64/int64(time.Second) {
		return 0, ErrInvalidConfig
	}
	return time.Duration(seconds) * time.Second, nil
}

func (c *Client) invalidate(ctx context.Context) error {
	if err := contextOK(ctx); err != nil {
		return err
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	err := c.cache.Delete(ctx, manifestKeyPrefix+c.domain)
	c.epoch.Add(1)
	c.index.Store(nil)
	return err
}

// Close cancels source work.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.cancel()
	})
	return nil
}
