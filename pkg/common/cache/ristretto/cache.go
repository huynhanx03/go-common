package ristretto

import (
	"context"
	"time"

	"github.com/huynhanx03/go-common/pkg/hash"
)

func (c *Cache[K, V]) write(key K, value any, ttl time.Duration) bool {
	key = dataCacheKey(key)
	var accepted bool
	if ttl == 0 {
		accepted = c.inner.Set(key, value, defaultCost)
	} else {
		accepted = c.inner.SetWithTTL(key, value, defaultCost, ttl)
	}
	c.inner.Wait()
	return accepted
}

func (c *Cache[K, V]) delete(keys ...K) {
	for _, key := range keys {
		c.inner.Del(dataCacheKey(key))
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
	value, ok := c.inner.Get(dataCacheKey(key))
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
	return c.write(key, value, 0)
}

// SetWithTTL adds or updates a value with a TTL.
func (c *Cache[K, V]) SetWithTTL(ctx context.Context, key K, value V, ttl time.Duration) bool {
	if ttl <= 0 || ctx.Err() != nil {
		return false
	}
	token := c.mu.RLock()
	defer c.mu.RUnlock(token)
	return c.write(key, value, ttl)
}

// Delete removes a value from the cache.
func (c *Cache[K, V]) Delete(key K) {
	token := c.mu.RLock()
	defer c.mu.RUnlock(token)
	c.delete(key)
}

// Clear removes all items from the cache.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inner.Clear()
}

func dataCacheKey[K hash.Key](key K) K {
	if value, ok := any(key).(string); ok {
		return any(dataKeyPrefix + value).(K)
	}
	return key
}
