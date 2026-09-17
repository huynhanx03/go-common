package ristretto

import (
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/hash"
)

// defaultCost is used for all ristretto Set/SetWithTTL calls.
const defaultCost int64 = 1

// Cache wraps *ristretto.Cache and implements cache.LocalCache[K, V].
type Cache[K hash.Key, V any] struct {
	inner  *ristretto.Cache
	mu     sync.RWMutex
	once   sync.Once
	closed bool
}

var _ cache.LocalCache[string, any] = (*Cache[string, any])(nil)

// New creates a new Ristretto-backed Cache[K, V].
// It applies the given options on top of DefaultConfig and then
// initialises the underlying ristretto cache.
func New[K hash.Key, V any](opts ...Option) (*Cache[K, V], error) {
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
func hashKey[K hash.Key](key K) uint64 {
	return hash.Sum64(key)
}

// Get retrieves a value from the cache.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.inner == nil {
		var zero V
		return zero, false
	}
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.inner == nil {
		return false
	}
	ok := c.inner.Set(hashKey(key), value, defaultCost)
	c.inner.Wait()
	return ok
}

// SetWithTTL adds or updates a value with a TTL.
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.inner == nil || ttl <= 0 {
		return false
	}
	ok := c.inner.SetWithTTL(hashKey(key), value, defaultCost, ttl)
	c.inner.Wait()
	return ok
}

// Delete removes a value from the cache.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.inner == nil {
		return
	}
	c.inner.Del(hashKey(key))
}

// Clear removes all items from the cache.
func (c *Cache[K, V]) Clear() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.inner == nil {
		return
	}
	c.inner.Clear()
}

// Close gracefully shuts down the cache.
func (c *Cache[K, V]) Close() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		if c.inner != nil {
			c.inner.Close()
		}
	})
}

// Stats returns a snapshot of cache statistics, sourced from ristretto's
// metrics (enabled by DefaultConfig). Zero when metrics are disabled.
func (c *Cache[K, V]) Stats() cache.Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var s cache.Stats
	if c.closed || c.inner == nil {
		return s
	}
	if m := c.inner.Metrics; m != nil {
		s.Hits = int64(m.Hits())
		s.Misses = int64(m.Misses())
		s.Evictions = int64(m.KeysEvicted())
		s.KeyCount = int64(m.KeysAdded() - m.KeysEvicted())
		s.CostUsed = int64(m.CostAdded() - m.CostEvicted())
	}
	return s
}
