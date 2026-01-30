package cache

import (
	"context"
	"time"
)

// LocalCache defines the interface for in-memory local cache operations.
type LocalCache[K comparable, V any] interface {
	Get(key K) (V, bool)
	Set(key K, value V, cost int64) bool
	Delete(key K)
	Clear()
	Close()
}

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
	GeoAdd(ctx context.Context, key string, locations ...*GeoLocation) error
	GeoRemove(ctx context.Context, key string, members ...string) error
	GeoRadius(ctx context.Context, key string, longitude, latitude, radius float64, unit string) ([]*GeoLocation, error)
	Close()
}
