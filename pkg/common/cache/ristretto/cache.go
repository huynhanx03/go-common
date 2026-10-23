package ristretto

import (
	"context"
	"time"

	"github.com/huynhanx03/go-common/pkg/hash"
)

const defaultCost int64 = 1

func hashKey[K hash.Key](key K) uint64 { return hash.Sum64(key) }

func (c *Cache[K, V]) write(key uint64, value any, ttl time.Duration) bool {
	var accepted bool
	if ttl == 0 {
		accepted = c.inner.Set(key, value, defaultCost)
	} else {
		accepted = c.inner.SetWithTTL(key, value, defaultCost, ttl)
	}
	c.inner.Wait()
	return accepted
}

func (c *Cache[K, V]) delete(keys ...uint64) {
	for _, key := range keys {
		c.inner.Del(key)
	}
	c.inner.Wait()
}

// Get retrieves a value from the cache.
func (c *Cache[K, V]) Get(ctx context.Context, key K) (V, bool) {
	var zero V
	if ctx.Err() != nil {
		return zero, false
	}
	token := c.mu.RLock()
	defer c.mu.RUnlock(token)
	value, ok := c.inner.Get(hashKey(key))
	if !ok {
		return zero, false
	}
	typed, ok := value.(V)
	if !ok {
		return zero, false
	}
	return typed, true
}

// Set adds or updates a value without a TTL.
func (c *Cache[K, V]) Set(key K, value V) bool {
	token := c.mu.RLock()
	defer c.mu.RUnlock(token)
	return c.write(hashKey(key), value, 0)
}

// SetWithTTL adds or updates a value with a TTL.
func (c *Cache[K, V]) SetWithTTL(ctx context.Context, key K, value V, ttl time.Duration) bool {
	if ttl <= 0 || ctx.Err() != nil {
		return false
	}
	token := c.mu.RLock()
	defer c.mu.RUnlock(token)
	return c.write(hashKey(key), value, ttl)
}

// Delete removes a value from the cache.
func (c *Cache[K, V]) Delete(key K) {
	token := c.mu.RLock()
	defer c.mu.RUnlock(token)
	c.delete(hashKey(key))
}

// Clear removes all items from the cache.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inner.Clear()
}
