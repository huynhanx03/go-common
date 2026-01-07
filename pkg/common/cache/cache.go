package cache

import (
	"context"
	"time"
)

// CacheEngine defines the standard interface for caching operations
type CacheEngine interface {
	// Get retrieves a value by key.
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// Set stores a value with an optional TTL.
	// value can be any serializable type.
	Set(ctx context.Context, key string, value any, ttl time.Duration) error

	// Delete removes a key from the cache.
	Delete(ctx context.Context, key string) error

	// InvalidatePrefix removes all keys matching a prefix.
	// Useful for clearing group caches.
	InvalidatePrefix(ctx context.Context, prefix string) error

	// BatchSet stores multiple values in a pipeline.
	// values is a map of key -> value.
	BatchSet(ctx context.Context, values map[string]any, ttl time.Duration) error

	// BatchDelete removes multiple keys from the cache.
	BatchDelete(ctx context.Context, keys []string) error

	// GeoAdd adds geospatial locations.
	GeoAdd(ctx context.Context, key string, locations ...*GeoLocation) error

	// GeoRemove removes members from a geospatial index.
	GeoRemove(ctx context.Context, key string, members ...string) error

	// GeoRadius searches for members within a radius.
	GeoRadius(ctx context.Context, key string, longitude, latitude, radius float64, unit string) ([]*GeoLocation, error)

	// Close closes the connection to the cache server.
	Close()
}
