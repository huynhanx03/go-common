package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newRateLimitRouter(t *testing.T, config RateLimitConfig) *gin.Engine {
	t.Helper()
	middleware, err := NewRateLimit(config)
	if err != nil {
		t.Fatalf("NewRateLimit: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router
}

func TestRateLimitIgnoresForwardingHeadersFromUntrustedPeer(t *testing.T) {
	t.Parallel()

	router := newRateLimitRouter(t, RateLimitConfig{
		Limit: 1, Burst: 1, Window: time.Hour, MaxKeys: 16, IdleExpiry: time.Hour,
	})
	for index, forwarded := range []string{"198.51.100.1", "198.51.100.2"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.113.10:1234"
		request.Header.Set("X-Forwarded-For", forwarded)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		want := http.StatusNoContent
		if index == 1 {
			want = http.StatusTooManyRequests
		}
		if recorder.Code != want {
			t.Fatalf("request %d status = %d, want %d", index, recorder.Code, want)
		}
	}
}

func TestRateLimitUsesForwardingChainOnlyForTrustedProxy(t *testing.T) {
	t.Parallel()

	router := newRateLimitRouter(t, RateLimitConfig{
		Limit:          1,
		Burst:          1,
		Window:         time.Hour,
		MaxKeys:        16,
		IdleExpiry:     time.Hour,
		TrustedProxies: []string{"203.0.113.0/24"},
	})
	for _, forwarded := range []string{"198.51.100.1", "198.51.100.2"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.113.10:1234"
		request.Header.Set("X-Forwarded-For", forwarded)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("trusted proxy client %s status = %d", forwarded, recorder.Code)
		}
	}
}

func TestNewRateLimitRejectsInvalidAndBoundsStorage(t *testing.T) {
	t.Parallel()

	for _, config := range []RateLimitConfig{
		{},
		{Limit: -1, Burst: 1, Window: time.Second},
		{Limit: 1, Burst: -1, Window: time.Second},
		{Limit: 1, Burst: 1, Window: -time.Second},
		{Limit: 1, Burst: 1, Window: time.Second, TrustedProxies: []string{"not-a-cidr"}},
	} {
		if _, err := NewRateLimit(config); err == nil {
			t.Fatalf("NewRateLimit accepted invalid config: %+v", config)
		}
	}

	limiter, err := NewRateLimiter(RateLimitConfig{
		Limit: 100, Burst: 100, Window: time.Minute, MaxKeys: 2, IdleExpiry: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, address := range []string{"192.0.2.1:1", "192.0.2.2:1", "192.0.2.3:1"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = address
		router.ServeHTTP(httptest.NewRecorder(), request)
	}
	limiter.mu.Lock()
	size := len(limiter.entries)
	limiter.mu.Unlock()
	if size != 2 {
		t.Fatalf("rate-limit entries = %d, want bounded at 2", size)
	}
}
