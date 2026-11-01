package casbin

import (
	"context"
	"strconv"
	"time"

	commoncache "github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

// RuntimeCache reuses the application's cache while keeping Casbin runtimes
// process-local. Each scope and policy revision has its own L1 entry.
type RuntimeCache struct {
	cache   *commoncache.TypedCache[*Runtime]
	ttl     time.Duration
	options runtimeOptions
}

// NewRuntimeCache binds a Casbin runtime cache to the application cache.
func NewRuntimeCache(cache *commoncache.Cache, ttl time.Duration, optionFunctions ...Option) (*RuntimeCache, error) {
	if cache == nil || !cache.HasL1() || ttl <= 0 {
		return nil, ErrInvalidCache
	}
	options, err := configuredRuntimeOptions(optionFunctions...)
	if err != nil {
		return nil, err
	}
	return &RuntimeCache{
		cache:   commoncache.For[*Runtime](cache),
		ttl:     ttl,
		options: options,
	}, nil
}

// Fetch returns the runtime for a scope and revision, loading a complete
// snapshot on an L1 miss. Runtime values never leave the current process.
func (c *RuntimeCache) Fetch(
	ctx context.Context,
	scope authorization.Scope,
	revision int64,
	load func(context.Context) (Snapshot, error),
) (*Runtime, error) {
	if scope.Validate() != nil {
		return nil, authorization.ErrInvalidRequest
	}
	if revision <= 0 || load == nil {
		return nil, ErrInvalidSnapshot
	}
	key := runtimeCacheKey(scope, revision)
	runtime, err := c.cache.Fetch(ctx, key, c.ttl, func(ctx context.Context) (*Runtime, error) {
		snapshot, err := load(ctx)
		if err != nil {
			return nil, err
		}
		if snapshot.Revision != revision {
			return nil, ErrStaleRevision
		}
		return newRuntime(ctx, snapshot, c.options)
	}, commoncache.L1Only(), commoncache.WithoutRefresh())
	if err != nil {
		return nil, err
	}
	if runtime.AppliedRevision() != revision || runtime.Healthy() != nil {
		return nil, ErrUnhealthy
	}
	return runtime, nil
}

func runtimeCacheKey(scope authorization.Scope, revision int64) string {
	return scopedRuntimeKeyPrefix + scope.Key() + ":" + strconv.FormatInt(revision, 10)
}
