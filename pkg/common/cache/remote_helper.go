package cache

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

// GetRemote retrieves a typed value from remote cache. A missing key is a
// clean miss (false, nil error); decode failures and engine errors are errors.
func GetRemote[T any](ctx context.Context, c CacheEngine, key string) (T, bool, error) {
	var zero T

	data, exists, err := c.Get(ctx, key)
	if !exists {
		if err != nil && !errors.Is(err, ErrKeyNotFound) {
			return zero, false, err
		}
		return zero, false, nil
	}

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return zero, false, err
	}
	return value, true, nil
}

// SetRemote stores a value in remote cache with TTL.
func SetRemote[T any](ctx context.Context, c CacheEngine, key string, value T, ttl time.Duration) error {
	return c.Set(ctx, key, value, ttl)
}

// DeleteRemote removes a key from remote cache.
func DeleteRemote(ctx context.Context, c CacheEngine, key string) error {
	return c.Delete(ctx, key)
}

// FetchRemote retrieves a cached value, loading it with fn on miss. It bundles
// the full anti-stampede toolkit:
//
//   - singleflight — concurrent misses trigger a single fn call per instance;
//   - TTL jitter (±10%) — keys written in the same burst don't expire together;
//   - probabilistic early expiration (XFetch) — hits near the end of the TTL
//     refresh the key in the background while the cached value is returned
//     immediately; the per-request randomization spreads refreshes across
//     instances, so hot keys never expire mid-traffic;
//   - negative caching — when fn reports cache.ErrNotFound, that outcome is
//     cached for NegativeTTL and lookups of nonexistent IDs stop reaching the
//     source.
//
// Keys written by FetchRemote are internal envelopes — read them through
// FetchRemote, not GetRemote.
func FetchRemote[T any](
	ctx context.Context,
	c CacheEngine,
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	var zero T

	load := func(ctx context.Context) (T, error) {
		start := time.Now()
		value, err := fn(ctx)
		switch {
		case err == nil:
			jttl := jitterTTL(ttl)
			_ = SetRemote(ctx, c, key, newEnvelope(value, jttl, time.Since(start)), jttl)
		case errors.Is(err, ErrNotFound):
			_ = DeleteRemote(ctx, c, key)
			_ = SetRemote(ctx, c, key+negativeSuffix, true, jitterTTL(NegativeTTL))
		}
		return value, err
	}

	if env, ok, _ := GetRemote[envelope[T]](ctx, c, key); ok {
		if env.shouldRefresh() {
			// Detach from the request's cancellation but keep its values (cid…).
			bgCtx := context.WithoutCancel(ctx)
			refreshAsync(sf, key, func() (T, error) {
				return load(bgCtx)
			})
		}
		return env.Value, nil
	}
	if _, neg, _ := GetRemote[bool](ctx, c, key+negativeSuffix); neg {
		return zero, ErrNotFound
	}

	return doTyped(sf, key, func() (T, error) {
		if env, ok, _ := GetRemote[envelope[T]](ctx, c, key); ok {
			return env.Value, nil
		}
		if _, neg, _ := GetRemote[bool](ctx, c, key+negativeSuffix); neg {
			return zero, ErrNotFound
		}
		return load(ctx)
	})
}
