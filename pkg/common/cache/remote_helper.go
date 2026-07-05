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

// FetchRemote retrieves a cached value. On miss, calls fn, caches the result
// with TTL (±10% jitter), and returns it. Singleflight deduplicates
// concurrent loads.
//
// When fn reports cache.ErrNotFound, that outcome is cached for NegativeTTL —
// repeated lookups of nonexistent IDs stop reaching the source.
func FetchRemote[T any](
	ctx context.Context,
	c CacheEngine,
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	var zero T

	if value, ok, _ := GetRemote[T](ctx, c, key); ok {
		return value, nil
	}
	if _, neg, _ := GetRemote[bool](ctx, c, key+negativeSuffix); neg {
		return zero, ErrNotFound
	}

	return doTyped(sf, key, func() (T, error) {
		if value, ok, _ := GetRemote[T](ctx, c, key); ok {
			return value, nil
		}
		if _, neg, _ := GetRemote[bool](ctx, c, key+negativeSuffix); neg {
			return zero, ErrNotFound
		}

		value, err := fn(ctx)
		switch {
		case err == nil:
			_ = SetRemote(ctx, c, key, value, jitterTTL(ttl))
		case errors.Is(err, ErrNotFound):
			_ = SetRemote(ctx, c, key+negativeSuffix, true, jitterTTL(NegativeTTL))
		}
		return value, err
	})
}

// FetchRemoteWithRefresh is FetchRemote plus probabilistic early expiration
// (XFetch): every hit independently decides — with probability rising as the
// TTL nears its end, and earlier for values that are slow to compute — to
// refresh the key in the background while the cached value is returned
// immediately. Hot keys never expire mid-traffic, and because the decision is
// randomized per request, refreshes spread out across instances.
//
// Keys written by FetchRemoteWithRefresh are internal envelopes — read them
// through FetchRemoteWithRefresh, not GetRemote.
func FetchRemoteWithRefresh[T any](
	ctx context.Context,
	c CacheEngine,
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	load := func(ctx context.Context) (T, error) {
		start := time.Now()
		value, err := fn(ctx)
		if err == nil {
			jttl := jitterTTL(ttl)
			_ = SetRemote(ctx, c, key, newEnvelope(value, jttl, time.Since(start)), jttl)
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

	return doTyped(sf, key, func() (T, error) {
		if env, ok, _ := GetRemote[envelope[T]](ctx, c, key); ok {
			return env.Value, nil
		}
		return load(ctx)
	})
}
