package cache

import (
	"errors"
	"time"

	"golang.org/x/sync/singleflight"
)

// Get retrieves a typed value from local cache.
func Get[T any](c LocalCache[string, any], key string) (T, bool) {
	val, found := c.Get(key)
	if found {
		if typed, ok := val.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

// Set stores a value in local cache without TTL.
func Set[T any](c LocalCache[string, any], key string, value T) bool {
	return c.Set(key, any(value))
}

// SetWithTTL stores a value with TTL.
func SetWithTTL[T any](c LocalCache[string, any], key string, value T, ttl time.Duration) bool {
	return c.SetWithTTL(key, any(value), ttl)
}

// Delete removes a key from local cache.
func Delete(c LocalCache[string, any], key string) {
	c.Delete(key)
}

// Fetch retrieves a cached value. On miss, calls fn, caches the result with
// TTL (±10% jitter), and returns it. Singleflight deduplicates concurrent
// loads: only one goroutine calls fn, the rest wait for its result.
//
// When fn reports cache.ErrNotFound, that outcome is cached for NegativeTTL —
// repeated lookups of nonexistent IDs stop reaching the source.
func Fetch[T any](
	c LocalCache[string, any],
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	fn func() (T, error),
) (T, error) {
	var zero T

	if val, ok := Get[T](c, key); ok {
		return val, nil
	}
	if _, neg := Get[negativeMarker](c, key+negativeSuffix); neg {
		return zero, ErrNotFound
	}

	return doTyped(sf, key, func() (T, error) {
		if val, ok := Get[T](c, key); ok {
			return val, nil
		}
		if _, neg := Get[negativeMarker](c, key+negativeSuffix); neg {
			return zero, ErrNotFound
		}

		value, err := fn()
		switch {
		case err == nil:
			SetWithTTL(c, key, value, jitterTTL(ttl))
		case errors.Is(err, ErrNotFound):
			SetWithTTL(c, key+negativeSuffix, negativeMarker{}, jitterTTL(NegativeTTL))
		}
		return value, err
	})
}

// FetchWithRefresh is Fetch plus probabilistic early expiration (XFetch):
// every hit independently decides — with probability rising as the TTL nears
// its end, and earlier for values that are slow to compute — to refresh the
// key in the background while the cached value is returned immediately. Hot
// keys never expire mid-traffic: no cold miss, no stampede.
//
// Keys written by FetchWithRefresh are internal envelopes — read them through
// FetchWithRefresh, not Get.
func FetchWithRefresh[T any](
	c LocalCache[string, any],
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	fn func() (T, error),
) (T, error) {
	load := func() (T, error) {
		start := time.Now()
		value, err := fn()
		if err == nil {
			jttl := jitterTTL(ttl)
			SetWithTTL(c, key, newEnvelope(value, jttl, time.Since(start)), jttl)
		}
		return value, err
	}

	if env, ok := Get[envelope[T]](c, key); ok {
		if env.shouldRefresh() {
			refreshAsync(sf, key, load)
		}
		return env.Value, nil
	}

	return doTyped(sf, key, func() (T, error) {
		if env, ok := Get[envelope[T]](c, key); ok {
			return env.Value, nil
		}
		return load()
	})
}
