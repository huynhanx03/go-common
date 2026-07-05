package cache

import (
	"context"
	"time"

	"github.com/huynhanx03/go-common/pkg/dto"
)

// CacheEngine defines the standard interface for remote caching operations.
type CacheEngine interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	InvalidatePrefix(ctx context.Context, prefix string) error
	BatchSet(ctx context.Context, values map[string]any, ttl time.Duration) error
	DeleteBulk(ctx context.Context, keys []string) error
	Incr(ctx context.Context, key string) (int64, error)
	Decr(ctx context.Context, key string) (int64, error)
	SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
	GeoAdd(ctx context.Context, key string, locations ...*dto.GeoLocation) error
	GeoRemove(ctx context.Context, key string, members ...string) error
	GeoRadius(ctx context.Context, key string, longitude, latitude, radius float64, unit string) ([]*dto.GeoLocation, error)
	ZAdd(ctx context.Context, key string, members ...*dto.ZMember) error
	ZRemRangeByScore(ctx context.Context, key string, min, max string) error
	ZCount(ctx context.Context, key string, min, max string) (int64, error)
	ZRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	Keys(ctx context.Context, pattern string) ([]string, error)
	Close()
}
