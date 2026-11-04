package cache

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/huynhanx03/go-common/pkg/algorithm"
	"github.com/huynhanx03/go-common/pkg/timer"
)

var refreshSlots = make(chan struct{}, maxConcurrentRefreshes)

type fetcher[T any] struct {
	cache          *Cache
	key            string
	ttl            time.Duration
	loader         func(context.Context) (T, error)
	l1Only         bool
	withoutRefresh bool
}

// FetchOption changes how one Fetch call uses the configured cache tiers.
type FetchOption struct {
	l1Only         bool
	withoutRefresh bool
}

// L1Only keeps the value in process-local memory and bypasses L2 reads and
// writes.
func L1Only() FetchOption {
	return FetchOption{l1Only: true}
}

// WithoutRefresh disables XFetch background refresh for one Fetch call.
func WithoutRefresh() FetchOption {
	return FetchOption{withoutRefresh: true}
}

// TypedCache binds a value type to a shared cache backend.
type TypedCache[T any] struct {
	cache *Cache
}

// For returns a type-safe view of a cache backend.
func For[T any](cache *Cache) *TypedCache[T] {
	return &TypedCache[T]{cache: cache}
}

// Fetch returns a cached value or loads it through the configured tiers. For
// L2, T selects decoding; []byte returns the stored payload as raw bytes.
func (c *TypedCache[T]) Fetch(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (T, error), options ...FetchOption) (T, error) {
	return fetch(ctx, c.cache, key, ttl, loader, options...)
}

// Fetch is the generic-function form of TypedCache.Fetch.
func Fetch[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, loader func(context.Context) (T, error), options ...FetchOption) (T, error) {
	return fetch(ctx, c, key, ttl, loader, options...)
}

func fetch[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, loader func(context.Context) (T, error), options ...FetchOption) (T, error) {
	var zero T
	if key == "" || len(key) > MaxKeyBytes || ttl <= 0 {
		return zero, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	var fetchOptions FetchOption
	for _, option := range options {
		fetchOptions.l1Only = fetchOptions.l1Only || option.l1Only
		fetchOptions.withoutRefresh = fetchOptions.withoutRefresh || option.withoutRefresh
	}
	return (&fetcher[T]{
		cache:          c,
		key:            key,
		ttl:            ttl,
		loader:         loader,
		l1Only:         fetchOptions.l1Only,
		withoutRefresh: fetchOptions.withoutRefresh,
	}).run(ctx)
}

func (f *fetcher[T]) run(ctx context.Context) (T, error) {
	var zero T
	if value, ok := f.read(ctx); ok {
		if !f.withoutRefresh && value.shouldRefresh() {
			refreshCtx, cancel := detachedContext(ctx)
			if !refreshAsync(&f.cache.sf, f.key, func() (T, error) {
				defer cancel()
				return f.load(refreshCtx)
			}) {
				cancel()
			}
		}
		return value.Value, nil
	}
	if f.readNegative(ctx) {
		return zero, ErrNotFound
	}
	return doTypedContext(ctx, f.singleflight(), f.key, func() (T, error) {
		if value, ok := f.read(ctx); ok {
			return value.Value, nil
		}
		if f.readNegative(ctx) {
			return zero, ErrNotFound
		}
		return f.load(ctx)
	})
}

func (f *fetcher[T]) read(ctx context.Context) (entry[T], bool) {
	if value, ok := f.cache.tiers.getL1(ctx, f.key); ok {
		if cached, valid := value.(entry[T]); valid && !cached.Negative && time.Until(time.UnixMilli(cached.ExpireAt)) > 0 {
			return cached, true
		}
	}
	if f.l1Only {
		return entry[T]{}, false
	}

	data, l1Version, found := f.cache.tiers.getL2ForPromotion(ctx, f.key, f.key, operationL2Get)
	if !found {
		return entry[T]{}, false
	}
	cached, err := decodeEntry[T](data)
	if err != nil {
		f.cache.tiers.reportError(ctx, operationL2Decode, err)
		return entry[T]{}, false
	}
	remaining := time.Until(time.UnixMilli(cached.ExpireAt))
	if remaining <= 0 {
		return entry[T]{}, false
	}
	f.cache.tiers.setL1(ctx, f.key, f.key, cached, remaining, l1Version, operationL1Promote)
	return cached, true
}

func (f *fetcher[T]) readNegative(ctx context.Context) bool {
	negativeKey := f.key + NegativeKeySuffix
	if _, ok := f.cache.tiers.getL1(ctx, negativeKey); ok {
		return true
	}
	if f.l1Only {
		return false
	}

	data, l1Version, found := f.cache.tiers.getL2ForPromotion(ctx, f.key, negativeKey, operationL2GetNegative)
	if !found {
		return false
	}
	expiresAt, err := decodeNegativeEntry(data)
	if err != nil {
		f.cache.tiers.reportError(ctx, operationL2DecodeNegative, err)
		return false
	}
	remaining := time.Until(expiresAt)
	if remaining <= 0 {
		return false
	}
	f.cache.tiers.setL1(ctx, f.key, negativeKey, entry[struct{}]{ExpireAt: expiresAt.UnixMilli(), Negative: true}, remaining, l1Version, operationL1PromoteNegative)
	return true
}

func (f *fetcher[T]) load(ctx context.Context) (T, error) {
	var zero T
	versions := tierVersions{l1: f.cache.tiers.snapshotL1(ctx, f.key)}
	if !f.l1Only {
		versions.l2 = f.cache.tiers.snapshotL2(ctx, f.key)
	}

	started := time.Now()
	value, err := f.loader(ctx)
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	switch {
	case err == nil:
		f.store(ctx, value, time.Since(started), versions)
	case errors.Is(err, ErrNotFound):
		f.cache.tiers.storeNegative(ctx, f.key, jitterTTL(NegativeTTL), versions)
	}
	return value, err
}

func (f *fetcher[T]) store(ctx context.Context, value T, loadTime time.Duration, versions tierVersions) {
	ttl := jitterTTL(f.ttl)
	cached := newEntry(value, ttl, loadTime)
	f.cache.tiers.setL1(ctx, f.key, f.key, cached, ttl, versions.l1, operationL1Set)
	storeEntryL2(ctx, f.cache.tiers, f.key, cached, ttl, versions.l2)
}

func (f *fetcher[T]) singleflight() *singleflight.Group {
	if f.l1Only {
		return &f.cache.l1sf
	}
	return &f.cache.sf
}

func doTypedContext[T any](ctx context.Context, sf *singleflight.Group, key string, fn func() (T, error)) (T, error) {
	result := sf.DoChan(key, func() (any, error) { return fn() })
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case outcome := <-result:
		if outcome.Err != nil {
			var zero T
			return zero, outcome.Err
		}
		return outcome.Val.(T), nil
	}
}

func detachedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}
	return detached, func() {}
}

func refreshAsync[T any](sf *singleflight.Group, key string, load func() (T, error)) bool {
	select {
	case refreshSlots <- struct{}{}:
	default:
		return false
	}
	go func() {
		defer func() { <-refreshSlots }()
		_, _, _ = sf.Do(key+refreshKeySuffix, func() (any, error) { return load() })
	}()
	return true
}

func jitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	return timer.JitterDuration(ttl, cacheTTLJitterFraction, rand.Float64())
}

func (e entry[T]) shouldRefresh() bool {
	return algorithm.XFetchShouldRefresh(
		time.UnixMilli(e.ExpireAt),
		timer.ToDurationMs(e.DeltaMs),
		xfetchBeta,
	)
}
