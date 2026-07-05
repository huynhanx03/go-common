package ristretto

import (
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/hash"
)

// defaultCost is used for all ristretto Set/SetWithTTL calls.
const defaultCost int64 = 1

// Cache wraps *ristretto.Cache and implements cache.LocalCache[K, V].
type Cache[K any, V any] struct {
	inner *ristretto.Cache
}

var _ cache.LocalCache[string, any] = (*Cache[string, any])(nil)

// New creates a new Ristretto-backed Cache[K, V].
// It applies the given options on top of DefaultConfig and then
// initialises the underlying ristretto cache.
func New[K any, V any](opts ...Option) (*Cache[K, V], error) {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	inner, err := ristretto.NewCache(&cfg)
	if err != nil {
		return nil, err
	}

	return &Cache[K, V]{
		inner: inner,
	}, nil
}

// hashKey converts a generic key to the uint64 that ristretto expects.
func hashKey[K any](key K) uint64 {
	h, _ := hash.KeyToHash(key)
	return h
}

// Get retrieves a value from the cache.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	val, ok := c.inner.Get(hashKey(key))
	if !ok {
		var zero V
		return zero, false
	}

	typed, ok := val.(V)
	if !ok {
		var zero V
		return zero, false
	}
	return typed, true
}

// Set adds or updates a value without TTL.
func (c *Cache[K, V]) Set(key K, value V) bool {
	ok := c.inner.Set(hashKey(key), value, defaultCost)
	c.inner.Wait()
	return ok
}

// SetWithTTL adds or updates a value with a TTL.
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) bool {
	ok := c.inner.SetWithTTL(hashKey(key), value, defaultCost, ttl)
	c.inner.Wait()
	return ok
}

// Delete removes a value from the cache.
func (c *Cache[K, V]) Delete(key K) {
	c.inner.Del(hashKey(key))
}

// Clear removes all items from the cache.
func (c *Cache[K, V]) Clear() {
	c.inner.Clear()
}

// Close gracefully shuts down the cache.
func (c *Cache[K, V]) Close() {
	c.inner.Close()
}

// Stats returns a snapshot of cache statistics, sourced from ristretto's
// metrics (enabled by DefaultConfig). Zero when metrics are disabled.
func (c *Cache[K, V]) Stats() cache.Stats {
	var s cache.Stats
	if m := c.inner.Metrics; m != nil {
		s.Hits = int64(m.Hits())
		s.Misses = int64(m.Misses())
		s.Evictions = int64(m.KeysEvicted())
		s.KeyCount = int64(m.KeysAdded() - m.KeysEvicted())
		s.CostUsed = int64(m.CostAdded() - m.CostEvicted())
	}
	return s
}
