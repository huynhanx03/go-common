package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
)

// HandleHitCache handles cache hit
func HandleHitCache(ctx context.Context, model any, c CacheEngine, key string) error {
	byteData, exists, err := c.Get(ctx, key)
	if exists && err == nil {
		err = json.Unmarshal(byteData, model)
		if err != nil {
			return errors.Wrap(err, "failed to unmarshal cache")
		}
		return nil
	}
	return errors.Wrap(err, "miss cache")
}

// HandleSetCache handles cache set
func HandleSetCache(ctx context.Context, model any, c CacheEngine, key string, ttl time.Duration) error {
	return c.Set(ctx, key, model, ttl)
}

// HandleUpdateCache handles cache update
func HandleUpdateCache(ctx context.Context, model any, c CacheEngine, key string, ttl time.Duration) {
	if _, exists, err := c.Get(ctx, key); err == nil && exists {
		_ = HandleSetCache(ctx, model, c, key, ttl)
	}
}

// HandleDeleteCache handles cache delete
func HandleDeleteCache(ctx context.Context, c CacheEngine, key string) error {
	return c.Delete(ctx, key)
}

// GetLocal retrieves a value from LocalCache and asserts its type.
func GetLocal[T any](c LocalCache[string, any], key string) (T, bool) {
	var zero T
	val, found := c.Get(key)
	if !found {
		return zero, false
	}
	// Direct type assertion since Cache is any
	if typed, ok := val.(T); ok {
		return typed, true
	}
	return zero, false
}

// SetLocal sets a value in LocalCache.
func SetLocal[T any](c LocalCache[string, any], key string, value T, cost int64) bool {
	return c.Set(key, any(value), cost)
}

// UpdateLocal helper updates an item in the cache only if it already exists.
func UpdateLocal[T any](c LocalCache[string, any], key string, value T, cost int64) {
	if _, found := GetLocal[T](c, key); found {
		SetLocal(c, key, value, cost)
	}
}

// DeleteLocal deletes a value from local cache.
func DeleteLocal(c LocalCache[string, any], key string) {
	c.Delete(key)
}
