package cache

import (
	"context"
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

// Fetch retrieves a cached value, loading it with fn on miss. It bundles the
// full anti-stampede toolkit:
//
//   - singleflight — concurrent misses trigger a single fn call;
//   - TTL jitter (±10%) — keys written in the same burst don't expire together;
//   - probabilistic early expiration (XFetch) — hits near the end of the TTL
//     refresh the key in the background while the cached value is returned
//     immediately, so hot keys never expire mid-traffic;
//   - negative caching — when fn reports cache.ErrNotFound, that outcome is
//     cached for NegativeTTL and lookups of nonexistent IDs stop reaching the
//     source.
//
// Keys written by Fetch are internal envelopes — read them through Fetch, not
// Get.
func Fetch[T any](
	c LocalCache[string, any],
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	fn func() (T, error),
) (T, error) {
	return GetOrLoad(
		context.Background(),
		c,
		sf,
		key,
		ttl,
		func(context.Context) (T, error) {
			return fn()
		},
	)
}

// GetOrLoad is the context-aware local cache-aside contract. Concurrent misses
// share one loader invocation while each waiter may cancel independently.
func GetOrLoad[T any](
	ctx context.Context,
	c LocalCache[string, any],
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	fn func(context.Context) (T, error),
) (T, error) {
	var zero T
	if ctx == nil ||
		c == nil ||
		sf == nil ||
		key == "" ||
		len(key) > 1024 ||
		ttl <= 0 ||
		fn == nil {
		return zero, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	load := func(loadContext context.Context) (T, error) {
		start := time.Now()
		value, err := fn(loadContext)
		if ctxErr := loadContext.Err(); ctxErr != nil {
			return zero, ctxErr
		}
		switch {
		case err == nil:
			jttl := jitterTTL(ttl)
			SetWithTTL(c, key, newEnvelope(value, jttl, time.Since(start)), jttl)
		case errors.Is(err, ErrNotFound):
			Delete(c, key)
			SetWithTTL(c, key+negativeSuffix, negativeMarker{}, jitterTTL(NegativeTTL))
		}
		return value, err
	}

	if env, ok := Get[envelope[T]](c, key); ok {
		if env.shouldRefresh() {
			refreshContext, cancel := detachedContext(ctx)
			if !refreshAsync(sf, key, func() (T, error) {
				defer cancel()
				return load(refreshContext)
			}) {
				cancel()
			}
		}
		return env.Value, nil
	}
	if _, neg := Get[negativeMarker](c, key+negativeSuffix); neg {
		return zero, ErrNotFound
	}

	return doTypedContext(ctx, sf, key, func() (T, error) {
		if env, ok := Get[envelope[T]](c, key); ok {
			return env.Value, nil
		}
		if _, neg := Get[negativeMarker](c, key+negativeSuffix); neg {
			return zero, ErrNotFound
		}
		return load(ctx)
	})
}
