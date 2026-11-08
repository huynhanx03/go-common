package middlewares

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRateLimitEnforcesQuotaAndHonorsSkip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Unix(1_800_000_000, 0)
	limiter, err := NewRateLimiter(RateLimitConfig{
		Limit: 1, Burst: 1, Window: time.Minute,
		KeyFunc: func(c *gin.Context) string { return c.GetHeader("X-User") },
		Skip:    func(c *gin.Context) bool { return c.Request.URL.Path == "/health" },
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	router := gin.New()
	router.Use(limiter.Middleware())
	router.Any("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User", "user-1")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	if got := request("/health"); got.Code != http.StatusNoContent {
		t.Fatalf("skipped request status = %d", got.Code)
	}
	allowed := request("/api")
	if allowed.Code != http.StatusNoContent || allowed.Header().Get(headerRateRemaining) != "0" {
		t.Fatalf("first request status/remaining = %d/%q", allowed.Code, allowed.Header().Get(headerRateRemaining))
	}
	limited := request("/api")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get(headerRetryAfter) != "60" {
		t.Fatalf("limited request status/retry-after = %d/%q", limited.Code, limited.Header().Get(headerRetryAfter))
	}
	if limited.Header().Get(headerRateLimit) != "1" || limited.Header().Get(headerRateReset) == "" {
		t.Fatalf("rate-limit metadata missing: %v", limited.Header())
	}
}

func TestRateLimitBoundsLongIdentityAndFailsClosedOnInvalidFacadeConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	longIdentity := strings.Repeat("u", maxRateLimitKeyBytes+1)
	limiter, err := NewRateLimiter(RateLimitConfig{
		Limit: 1, Burst: 1, Window: time.Hour,
		KeyFunc: func(*gin.Context) string { return longIdentity },
	})
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for index, want := range []int{http.StatusNoContent, http.StatusTooManyRequests} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != want {
			t.Fatalf("request %d status = %d, want %d", index, recorder.Code, want)
		}
	}

	bad := gin.New()
	bad.Use(RateLimit(RateLimitConfig{}))
	bad.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	bad.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("invalid compatibility configuration status = %d, want 500", recorder.Code)
	}
}

func TestRateLimitBoundsMalformedRemoteAddressIdentity(t *testing.T) {
	address := strings.Repeat("unparsed-peer", 40)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = address
	limiter, err := NewRateLimiter(RateLimitConfig{
		Limit: 1, Burst: 1, Window: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := limiter.clientIP(request)
	if len(identity) > maxRateLimitKeyBytes || strings.Contains(identity, address) {
		t.Fatalf("malformed remote address was not bounded: %q", identity)
	}
	if identity != limiter.clientIP(request) {
		t.Fatal("the same peer did not map to a stable rate-limit identity")
	}
	request.RemoteAddr = address + "-other"
	if identity == limiter.clientIP(request) {
		t.Fatal("distinct malformed peers shared a rate-limit identity")
	}
}

func TestRateLimitExpiresIdleEntriesBeforeEvictingActiveEntries(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	limiter, err := NewRateLimiter(RateLimitConfig{
		Limit: 100, Burst: 100, Window: time.Minute, MaxKeys: 1,
		IdleExpiry: time.Minute, Now: func() time.Time { return now },
		KeyFunc: func(c *gin.Context) string { return c.GetHeader("X-User") },
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := func(user string) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User", user)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}

	request("idle-user")
	now = now.Add(time.Minute)
	request("current-user")
	limiter.mu.Lock()
	_, idleStillStored := limiter.entries["idle-user"]
	_, currentStored := limiter.entries["current-user"]
	limiter.mu.Unlock()
	if idleStillStored || !currentStored {
		t.Fatalf("expired identity remained or replacement is missing: idle=%t current=%t", idleStillStored, currentStored)
	}

	now = now.Add(30 * time.Second)
	request("new-user")
	limiter.mu.Lock()
	_, currentStillStored := limiter.entries["current-user"]
	_, newStored := limiter.entries["new-user"]
	limiter.mu.Unlock()
	if currentStillStored || !newStored {
		t.Fatalf("full active table was not bounded by oldest-entry eviction: current=%t new=%t", currentStillStored, newStored)
	}
}
