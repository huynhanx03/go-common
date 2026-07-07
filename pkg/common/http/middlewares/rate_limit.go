package middlewares

import (
	"context"
	"fmt"

	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/algorithm"
	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/common/locks"
)

// Rate limiter defaults.
const (
	defaultRateLimit    = 100
	defaultRateBurst    = 100
	defaultRateWindow   = time.Minute
	defaultCleanupEvery = 5 * time.Minute
	defaultIdleExpiry   = 10 * time.Minute
)

// Rate limiter header names.
const (
	headerRateLimit     = "X-RateLimit-Limit"
	headerRateRemaining = "X-RateLimit-Remaining"
	headerRateReset     = "X-RateLimit-Reset"
	headerRetryAfter    = "Retry-After"
)

// RateLimitConfig configures the rate limiting middleware.
type RateLimitConfig struct {
	// Limit is the maximum number of requests per window.
	Limit int

	// Burst is the token bucket capacity (allows short bursts above the rate).
	Burst int

	// Window is the time window for the sliding window counter.
	Window time.Duration

	// KeyFunc extracts the rate limit key from the request (e.g., IP, user ID).
	// Defaults to client IP.
	KeyFunc func(*gin.Context) string

	// Skip returns true to bypass rate limiting for this request.
	Skip func(*gin.Context) bool

	// Ctx controls the cleanup goroutine lifetime. Defaults to context.Background().
	Ctx context.Context
}

// rateLimiterEntry holds a per-key limiter and its last access time.
type rateLimiterEntry struct {
	bucket   *algorithm.TokenBucket
	lastSeen time.Time
}

// RateLimit returns a Gin middleware that enforces per-key rate limiting
// using a token bucket algorithm.
func RateLimit(cfg RateLimitConfig) gin.HandlerFunc {
	if cfg.Limit <= 0 {
		cfg.Limit = defaultRateLimit
	}
	if cfg.Burst <= 0 {
		cfg.Burst = defaultRateBurst
	}
	if cfg.Window <= 0 {
		cfg.Window = defaultRateWindow
	}
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = func(c *gin.Context) string { return c.ClientIP() }
	}
	if cfg.Ctx == nil {
		cfg.Ctx = context.Background()
	}

	mu := locks.NewSpinLock()
	limiters := make(map[string]*rateLimiterEntry)

	// Background cleanup of stale limiters (stops when cfg.Ctx is cancelled).
	go func() {
		ticker := time.NewTicker(defaultCleanupEvery)
		defer ticker.Stop()
		for {
			select {
			case <-cfg.Ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				now := time.Now()
				for key, entry := range limiters {
					if now.Sub(entry.lastSeen) > defaultIdleExpiry {
						delete(limiters, key)
					}
				}
				mu.Unlock()
			}
		}
	}()

	return func(c *gin.Context) {
		if cfg.Skip != nil && cfg.Skip(c) {
			c.Next()
			return
		}

		key := cfg.KeyFunc(c)

		mu.Lock()
		entry, ok := limiters[key]
		if !ok {
			entry = &rateLimiterEntry{
				bucket: algorithm.NewTokenBucket(
					algorithm.WithBucketCapacity(cfg.Burst),
					algorithm.WithBucketFillRate(cfg.Limit, cfg.Window),
				),
			}
			limiters[key] = entry
		}
		entry.lastSeen = time.Now()
		mu.Unlock()

		// Try to consume a token first, then report accurate remaining count.
		allowed := entry.bucket.AllowOne()
		remaining := int(entry.bucket.Tokens())

		c.Header(headerRateLimit, strconv.Itoa(cfg.Limit))
		c.Header(headerRateRemaining, strconv.Itoa(remaining))
		c.Header(headerRateReset, strconv.FormatInt(time.Now().Add(cfg.Window).Unix(), 10))

		if !allowed {
			retryAfter := cfg.Window.Seconds()
			c.Header(headerRetryAfter, fmt.Sprintf("%.0f", retryAfter))
			response.ErrorResponse(c, apperr.CodeTooManyRequests, apperr.New(
				apperr.CodeTooManyRequests,
				"rate limit exceeded",
				nil,
			))
			c.Abort()
			return
		}

		c.Next()
	}
}
