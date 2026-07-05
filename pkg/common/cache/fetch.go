package cache

// Shared machinery for the Fetch* helpers in local_helper.go / remote_helper.go.

import (
	"math/rand/v2"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/huynhanx03/go-common/pkg/algorithm"
)

// xfetchBeta tunes probabilistic early expiration: >1 refreshes more eagerly,
// <1 less. 1.0 is the XFetch paper's recommendation.
const xfetchBeta = 1.0

// NegativeTTL is how long a "entity does not exist" outcome is cached.
// Short by design: existence can change at any moment, and its only job is
// to absorb bursts of lookups for missing IDs (cache penetration).
var NegativeTTL = 30 * time.Second

// negativeSuffix namespaces the marker key for negative caching, so the real
// key keeps its plain representation.
const negativeSuffix = ":neg"

// negativeMarker is the value stored under key+negativeSuffix.
type negativeMarker struct{}

// jitterTTL randomizes a TTL by ±10% so keys written in the same burst don't
// expire in the same instant (cache avalanche). Applied on every write made
// by the Fetch helpers; explicit Set/SetRemote calls honor the exact TTL.
func jitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	return time.Duration(float64(ttl) * (0.9 + 0.2*rand.Float64()))
}

// envelope wraps a cached value with the data the probabilistic early
// expiration decision needs: when the key really expires and how long the
// value took to compute. JSON tags keep the remote representation compact;
// the local cache stores the struct as-is.
type envelope[T any] struct {
	Value    T     `json:"v"`
	ExpireAt int64 `json:"e"` // unix milliseconds
	DeltaMs  int64 `json:"d"` // how long fn took, milliseconds
}

func newEnvelope[T any](value T, ttl, delta time.Duration) envelope[T] {
	return envelope[T]{
		Value:    value,
		ExpireAt: time.Now().Add(ttl).UnixMilli(),
		DeltaMs:  delta.Milliseconds(),
	}
}

// shouldRefresh decides whether a hit on this entry should trigger a
// background refresh (XFetch).
func (e envelope[T]) shouldRefresh() bool {
	return algorithm.XFetchShouldRefresh(
		time.UnixMilli(e.ExpireAt),
		time.Duration(e.DeltaMs)*time.Millisecond,
		xfetchBeta,
	)
}

// doTyped runs fn at most once per key across concurrent callers
// (singleflight) and returns the typed result.
func doTyped[T any](sf *singleflight.Group, key string, fn func() (T, error)) (T, error) {
	v, err, _ := sf.Do(key, func() (any, error) {
		return fn()
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

// refreshAsync reloads a key in the background, deduplicated per key so a
// burst of hits triggers a single reload.
func refreshAsync[T any](sf *singleflight.Group, key string, load func() (T, error)) {
	go func() {
		_, _, _ = sf.Do(key+":refresh", func() (any, error) {
			return load()
		})
	}()
}
