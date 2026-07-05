package cache

import "time"

// Stats holds hit/miss and other cache metrics.
type Stats struct {
	Hits        int64
	Misses      int64
	Evictions   int64
	ExpiredKeys int64
	KeyCount    int64
	CostUsed    int64
}

// LocalCache defines the interface for in-memory local cache operations.
// Implementations may offer more on their concrete type (counters, TTL
// inspection, batch ops…); the interface stays at what consumers need.
type LocalCache[K any, V any] interface {
	Get(key K) (V, bool)
	Set(key K, value V) bool
	SetWithTTL(key K, value V, ttl time.Duration) bool
	Delete(key K)
	Clear()
	Close()

	Stats() Stats
}
