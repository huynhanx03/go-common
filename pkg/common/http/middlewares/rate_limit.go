package middlewares

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/algorithm"
	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

const (
	defaultMaxRateLimitKeys = 10_000
	hardMaxRateLimitKeys    = 1_000_000
	defaultIdleExpiry       = 10 * time.Minute
	maxIdleExpiry           = 24 * time.Hour
	maxForwardedForBytes    = 4096
	maxForwardedHops        = 32
	maxRateLimitKeyBytes    = 256
)

const (
	headerRateLimit     = "X-RateLimit-Limit"
	headerRateRemaining = "X-RateLimit-Remaining"
	headerRateReset     = "X-RateLimit-Reset"
	headerRetryAfter    = "Retry-After"
)

type RateLimitConfig struct {
	Limit  int
	Burst  int
	Window time.Duration

	KeyFunc func(*gin.Context) string
	Skip    func(*gin.Context) bool

	// MaxKeys bounds per-identity storage. Zero uses 10,000.
	MaxKeys int
	// IdleExpiry controls opportunistic removal. Zero uses 10 minutes.
	IdleExpiry time.Duration
	// TrustedProxies contains exact IPs or CIDRs allowed to supply
	// X-Forwarded-For.
	TrustedProxies []string
	// Now is injectable for deterministic tests.
	Now func() time.Time

	// Deprecated: cleanup no longer owns a goroutine; storage is bounded and
	// cleaned opportunistically.
	Ctx context.Context
}

type rateLimiterEntry struct {
	bucket   *algorithm.TokenBucket
	lastSeen time.Time
}

// RateLimiter owns bounded per-key token buckets without a hidden cleanup
// goroutine.
type RateLimiter struct {
	mu             sync.Mutex
	config         RateLimitConfig
	entries        map[string]*rateLimiterEntry
	trustedProxies []netip.Prefix
}

func NewRateLimiter(config RateLimitConfig) (*RateLimiter, error) {
	if config.Limit <= 0 || config.Burst <= 0 || config.Window <= 0 {
		return nil, errors.New("rate limit: limit, burst, and window must be positive")
	}
	if config.MaxKeys < 0 ||
		config.MaxKeys > hardMaxRateLimitKeys ||
		config.IdleExpiry < 0 ||
		config.IdleExpiry > maxIdleExpiry {
		return nil, errors.New("rate limit: invalid storage bounds")
	}
	if config.MaxKeys == 0 {
		config.MaxKeys = defaultMaxRateLimitKeys
	}
	if config.IdleExpiry == 0 {
		config.IdleExpiry = defaultIdleExpiry
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	trusted, err := parseTrustedProxies(config.TrustedProxies)
	if err != nil {
		return nil, err
	}
	return &RateLimiter{
		config:         config,
		entries:        make(map[string]*rateLimiterEntry),
		trustedProxies: trusted,
	}, nil
}

// NewRateLimit constructs bounded rate-limit middleware.
func NewRateLimit(config RateLimitConfig) (gin.HandlerFunc, error) {
	limiter, err := NewRateLimiter(config)
	if err != nil {
		return nil, err
	}
	return limiter.Middleware(), nil
}

// RateLimit is a compatibility facade. Invalid configuration fails closed
// with a 500 response.
//
// Deprecated: call NewRateLimit and handle its error at bootstrap.
func RateLimit(config RateLimitConfig) gin.HandlerFunc {
	middleware, err := NewRateLimit(config)
	if err != nil {
		return func(c *gin.Context) { internalServerError(c) }
	}
	return middleware
}

func (limiter *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter == nil {
			internalServerError(c)
			return
		}
		if limiter.config.Skip != nil && limiter.config.Skip(c) {
			c.Next()
			return
		}

		key := limiter.identity(c)
		now := limiter.config.Now()
		entry := limiter.entry(key, now)
		allowed := entry.bucket.AllowOne()
		remaining := int(entry.bucket.Tokens())

		c.Header(headerRateLimit, strconv.Itoa(limiter.config.Limit))
		c.Header(headerRateRemaining, strconv.Itoa(remaining))
		c.Header(headerRateReset, strconv.FormatInt(now.Add(limiter.config.Window).Unix(), 10))
		if !allowed {
			retryAfter := max(int64(1), int64(limiter.config.Window/time.Second))
			c.Header(headerRetryAfter, strconv.FormatInt(retryAfter, 10))
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

func (limiter *RateLimiter) entry(key string, now time.Time) *rateLimiterEntry {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if existing, found := limiter.entries[key]; found {
		existing.lastSeen = now
		return existing
	}
	if len(limiter.entries) >= limiter.config.MaxKeys {
		limiter.removeExpired(now)
	}
	if len(limiter.entries) >= limiter.config.MaxKeys {
		limiter.removeOldest()
	}

	entry := &rateLimiterEntry{
		bucket: algorithm.NewTokenBucket(
			algorithm.WithBucketCapacity(limiter.config.Burst),
			algorithm.WithBucketFillRate(limiter.config.Limit, limiter.config.Window),
			algorithm.WithBucketClock(func() int64 { return limiter.config.Now().UnixNano() }),
		),
		lastSeen: now,
	}
	limiter.entries[key] = entry
	return entry
}

func (limiter *RateLimiter) removeExpired(now time.Time) {
	for key, entry := range limiter.entries {
		if now.Sub(entry.lastSeen) >= limiter.config.IdleExpiry {
			delete(limiter.entries, key)
		}
	}
}

func (limiter *RateLimiter) removeOldest() {
	var oldestKey string
	var oldestTime time.Time
	for key, entry := range limiter.entries {
		if oldestKey == "" || entry.lastSeen.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastSeen
		}
	}
	if oldestKey != "" {
		delete(limiter.entries, oldestKey)
	}
}

func (limiter *RateLimiter) identity(c *gin.Context) string {
	var key string
	if limiter.config.KeyFunc != nil {
		key = limiter.config.KeyFunc(c)
	} else {
		key = limiter.clientIP(c.Request)
	}
	if key == "" {
		key = "unknown"
	}
	if len(key) <= maxRateLimitKeyBytes {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (limiter *RateLimiter) clientIP(request *http.Request) string {
	peer, peerOK := parseRemoteAddress(request.RemoteAddr)
	if !peerOK {
		return boundedRemoteAddress(request.RemoteAddr)
	}
	if !limiter.trusted(peer) {
		return peer.String()
	}

	raw := request.Header.Get("X-Forwarded-For")
	if raw == "" || len(raw) > maxForwardedForBytes {
		return peer.String()
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxForwardedHops {
		return peer.String()
	}
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return peer.String()
		}
		chain = append(chain, address.Unmap())
	}

	current := peer
	for index := len(chain) - 1; index >= 0; index-- {
		if !limiter.trusted(current) {
			break
		}
		current = chain[index]
	}
	return current.String()
}

func (limiter *RateLimiter) trusted(address netip.Addr) bool {
	for _, prefix := range limiter.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func parseTrustedProxies(values []string) ([]netip.Prefix, error) {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("rate limit: empty trusted proxy")
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			result = append(result, prefix.Masked())
			continue
		}
		address, err := netip.ParseAddr(value)
		if err != nil {
			return nil, fmt.Errorf("rate limit: invalid trusted proxy %q", value)
		}
		address = address.Unmap()
		result = append(result, netip.PrefixFrom(address, address.BitLen()))
	}
	return result, nil
}

func parseRemoteAddress(value string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return netip.Addr{}, false
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func boundedRemoteAddress(value string) string {
	if len(value) <= maxRateLimitKeyBytes {
		return "remote:" + value
	}
	sum := sha256.Sum256([]byte(value))
	return "remote-sha256:" + hex.EncodeToString(sum[:])
}
