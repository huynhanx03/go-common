package http

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/utils"
)

type HTTPClientPool struct {
	client *http.Client
	mu     sync.RWMutex
	cache  map[string]any
}

type HTTPClientConfig struct {
	Timeout         time.Duration
	MaxIdleConns    int
	IdleConnTimeout time.Duration
	MaxConnsPerHost int
	EnableCache     bool
	CacheExpiration time.Duration
}

const (
	defaultTimeout         = 30 * time.Second
	defaultMaxIdleConns    = 100
	defaultIdleConnTimeout = 90 * time.Second
	defaultMaxConnsPerHost = 10
	defaultEnableCache     = true
	defaultCacheExpiration = 5 * time.Minute
)

// DefaultHTTPConfig returns default configuration for HTTP client pool
func DefaultHTTPConfig() *HTTPClientConfig {
	return &HTTPClientConfig{
		Timeout:         defaultTimeout,
		MaxIdleConns:    defaultMaxIdleConns,
		IdleConnTimeout: defaultIdleConnTimeout,
		MaxConnsPerHost: defaultMaxConnsPerHost,
		EnableCache:     defaultEnableCache,
		CacheExpiration: defaultCacheExpiration,
	}
}

// NewHTTPClientPool creates a new HTTP client pool with the given configuration
func NewHTTPClientPool(config *HTTPClientConfig) *HTTPClientPool {
	if config == nil {
		config = DefaultHTTPConfig()
	}

	client := &http.Client{
		Timeout: config.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:        config.MaxIdleConns,
			MaxIdleConnsPerHost: config.MaxConnsPerHost,
			IdleConnTimeout:     config.IdleConnTimeout,
		},
	}

	return &HTTPClientPool{
		client: client,
		cache:  make(map[string]any),
	}
}

// RequestWithRetry performs an HTTP request with retry logic
func (p *HTTPClientPool) RequestWithRetry(ctx context.Context, req *http.Request, maxRetries int) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			resp, err := p.client.Do(req)

			// Success case: No error and status is OK
			if err == nil {
				if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
					// Need retry
					resp.Body.Close() // Close body to prevent leak before retry
				} else {
					// Success
					return resp, nil
				}
			}

			if err != nil {
				lastErr = err
			}

			// Calculate backoff using shared utility with attempt cap
			waitDuration := utils.CalculateBackoffByAttempt(attempt, 1*time.Second, maxRetries)

			timer := time.NewTimer(waitDuration)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
				continue
			}
		}
	}
	return nil, lastErr
}

// GetFromCache retrieves data from cache if available
func (p *HTTPClientPool) GetFromCache(key string) (any, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	val, ok := p.cache[key]
	return val, ok
}

// SetCache stores data in cache
func (p *HTTPClientPool) SetCache(key string, value any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[key] = value
}

// ClearCache removes all items from cache
func (p *HTTPClientPool) ClearCache() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache = make(map[string]any)
}
