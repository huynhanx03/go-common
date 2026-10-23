package ristretto

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/unique"
)

const (
	generationTTL       = 24 * time.Hour
	fenceStripeCount    = 256
	generationKeyPrefix = "__cache_generation__:"
)

func generationKey(key string) string { return generationKeyPrefix + key }

func (c *Cache[K, V]) lockFence(key string) func() {
	mu := &c.fenceMu[hashKey(key)%fenceStripeCount]
	mu.Lock()
	return mu.Unlock
}

func (c *Cache[K, V]) lockVersion(ctx context.Context, key string) (string, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	lockToken := c.mu.RLock()
	if c.closed {
		c.mu.RUnlock(lockToken)
		return "", nil, errCacheClosed
	}

	versionKey := generationKey(key)
	unlockFence := c.lockFence(versionKey)
	return versionKey, func() {
		unlockFence()
		c.mu.RUnlock(lockToken)
	}, nil
}

// SnapshotVersion returns the current local fence token for key.
func (c *Cache[K, V]) SnapshotVersion(ctx context.Context, key string) (string, error) {
	if key == "" || len(key) > cache.MaxKeyBytes {
		return "", cache.ErrInvalidConfig
	}
	versionKey, unlock, err := c.lockVersion(ctx, key)
	if err != nil {
		return "", err
	}
	defer unlock()
	if value, ok := c.inner.Get(hashKey(versionKey)); ok {
		if token, ok := value.(string); ok && token != "" {
			return token, nil
		}
	}
	return c.writeGeneration(versionKey)
}

// SetWithVersion stores a value only while the captured fence token is current.
func (c *Cache[K, V]) SetWithVersion(ctx context.Context, versionKey, storageKey string, value any, ttl time.Duration, token string) error {
	if versionKey == "" || storageKey == "" || token == "" || ttl <= 0 {
		return cache.ErrInvalidConfig
	}
	versionKey, unlock, err := c.lockVersion(ctx, versionKey)
	if err != nil {
		return err
	}
	defer unlock()
	current, ok := c.inner.Get(hashKey(versionKey))
	if !ok || current != token {
		return nil
	}
	return c.writeVersionedValue(hashKey(storageKey), value, ttl)
}

// DeleteValueWithVersion deletes a value only while the captured token is current.
func (c *Cache[K, V]) DeleteValueWithVersion(ctx context.Context, versionKey, storageKey, token string) error {
	if versionKey == "" || storageKey == "" || token == "" {
		return cache.ErrInvalidConfig
	}
	versionKey, unlock, err := c.lockVersion(ctx, versionKey)
	if err != nil {
		return err
	}
	defer unlock()
	current, ok := c.inner.Get(hashKey(versionKey))
	if ok && current == token {
		c.delete(hashKey(storageKey))
	}
	return nil
}

// Revoke advances the fence and removes the value and its negative-cache entry.
func (c *Cache[K, V]) Revoke(ctx context.Context, key string) error {
	if key == "" || len(key) > cache.MaxKeyBytes {
		return cache.ErrInvalidConfig
	}
	versionKey, unlock, err := c.lockVersion(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := c.writeGeneration(versionKey); err != nil {
		return err
	}
	c.delete(hashKey(key), hashKey(key+cache.NegativeKeySuffix))
	return nil
}

// RevokeMany advances each key's fence and removes its value and negative entry.
func (c *Cache[K, V]) RevokeMany(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	for _, key := range keys {
		if key == "" || len(key) > cache.MaxKeyBytes {
			return cache.ErrInvalidConfig
		}
	}

	unlock, err := c.lockFences(ctx, keys)
	if err != nil {
		return err
	}
	defer unlock()

	var errs []error
	deleted := make([]string, 0, len(keys))
	for _, key := range keys {
		token, err := unique.NewToken()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !c.inner.SetWithTTL(hashKey(generationKey(key)), token, defaultCost, generationTTL) {
			errs = append(errs, errGenerationWriteRejected)
			continue
		}
		deleted = append(deleted, key, key+cache.NegativeKeySuffix)
	}
	if len(deleted) > 0 {
		c.inner.Wait()
		hashed := make([]uint64, 0, len(deleted))
		for _, key := range deleted {
			hashed = append(hashed, hashKey(key))
		}
		c.delete(hashed...)
	}
	return errors.Join(errs...)
}

func (c *Cache[K, V]) lockFences(ctx context.Context, keys []string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	readToken := c.mu.RLock()
	if c.closed {
		c.mu.RUnlock(readToken)
		return nil, errCacheClosed
	}

	stripes := make([]int, 0, len(keys))
	seen := make(map[int]struct{}, len(keys))
	for _, key := range keys {
		stripe := int(hashKey(generationKey(key)) % fenceStripeCount)
		if _, exists := seen[stripe]; exists {
			continue
		}
		seen[stripe] = struct{}{}
		stripes = append(stripes, stripe)
	}
	sort.Ints(stripes)
	for _, stripe := range stripes {
		c.fenceMu[stripe].Lock()
	}

	return func() {
		for index := len(stripes) - 1; index >= 0; index-- {
			c.fenceMu[stripes[index]].Unlock()
		}
		c.mu.RUnlock(readToken)
	}, nil
}

func (c *Cache[K, V]) writeGeneration(key string) (string, error) {
	token, err := unique.NewToken()
	if err != nil {
		return "", err
	}
	if !c.write(hashKey(key), token, generationTTL) {
		return "", errGenerationWriteRejected
	}
	return token, nil
}

func (c *Cache[K, V]) writeVersionedValue(key uint64, value any, ttl time.Duration) error {
	if !c.write(key, value, ttl) {
		return errConditionalWriteFailed
	}
	return nil
}
