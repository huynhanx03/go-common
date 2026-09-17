package http

import (
	"context"
	"net/http"
	"sync"
	"time"

	httpRequest "github.com/huynhanx03/go-common/pkg/common/http/request"
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

type RetryPolicy = httpRequest.RetryPolicy
type Attempt = httpRequest.Attempt

// DefaultRetryPolicy retries transient admission-control and upstream failures
// for methods that are safe to replay.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:       2,
		InitialBackoff:   time.Second,
		MaxBackoff:       30 * time.Second,
		JitterFraction:   0.2,
		RetryStatusCodes: []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
		RetryMethods:     []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions},
	}
}

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
		// CorrelationRoundTripper stamps the context's correlation ID onto every
		// outgoing request, so calls to other services keep the same cid.
		Transport: httpRequest.CorrelationRoundTripper(&http.Transport{
			MaxIdleConns:        config.MaxIdleConns,
			MaxIdleConnsPerHost: config.MaxConnsPerHost,
			IdleConnTimeout:     config.IdleConnTimeout,
		}),
	}

	return &HTTPClientPool{
		client: client,
		cache:  make(map[string]any),
	}
}

// Do executes through the pool's shared client and returns safe per-attempt
// metadata. The policy remains explicit at the call site.
func (p *HTTPClientPool) Do(
	ctx context.Context,
	request *http.Request,
	policy RetryPolicy,
) (*http.Response, []Attempt, error) {
	if p == nil {
		return nil, nil, httpRequest.ErrInvalidRequest
	}
	return httpRequest.Do(ctx, p.client, request, policy)
}

// RequestWithRetry is the compatibility convenience API. maxRetries counts
// attempts after the initial request, so zero still performs one request.
func (p *HTTPClientPool) RequestWithRetry(
	ctx context.Context,
	request *http.Request,
	maxRetries int,
) (*http.Response, error) {
	policy := DefaultRetryPolicy()
	policy.MaxRetries = maxRetries
	response, _, err := p.Do(ctx, request, policy)
	return response, err
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
