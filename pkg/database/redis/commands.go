package redis

import (
	"context"
	"errors"
	"time"

	redisV9 "github.com/redis/go-redis/v9"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

const (
	scanCount        = 500
	maxScannedKeys   = 100_000
	deleteBatchSize  = 500
	maxBatchCommands = 10_000
)

// Get returns the raw value stored at key.
func (r *RedisEngine) Get(ctx context.Context, key string) ([]byte, bool, error) {
	byteValue, err := r.client.Get(ctx, r.scopedKey(key)).Bytes()
	if err == redisV9.Nil {
		return nil, false, errors.Join(ErrKeyNotFound, cache.ErrKeyNotFound)
	}
	if err != nil {
		return nil, false, err
	}
	return byteValue, true, nil
}

// Delete removes key.
func (r *RedisEngine) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, r.scopedKey(key)).Err()
}

// Set JSON-encodes value and stores it at key.
func (r *RedisEngine) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	byteValue, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return r.client.Set(ctx, r.scopedKey(key), byteValue, ttl).Err()
}

// BatchSet JSON-encodes and stores values in one pipeline.
func (r *RedisEngine) BatchSet(ctx context.Context, values map[string]any, ttl time.Duration) error {
	if len(values) > maxBatchCommands {
		return ErrBatchTooLarge
	}
	type encodedValue struct {
		key   string
		value []byte
	}
	encoded := make([]encodedValue, 0, len(values))
	for key, value := range values {
		byteValue, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded = append(encoded, encodedValue{key: r.scopedKey(key), value: byteValue})
	}

	pipe := r.client.Pipeline()
	for _, item := range encoded {
		pipe.Set(ctx, item.key, item.value, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// DeleteBulk removes multiple keys.
func (r *RedisEngine) DeleteBulk(ctx context.Context, keys []string) error {
	if len(keys) > maxBatchCommands {
		return ErrBatchTooLarge
	}
	if len(keys) == 0 {
		return nil
	}
	scopedKeys := make([]string, len(keys))
	for i, key := range keys {
		scopedKeys[i] = r.scopedKey(key)
	}
	return r.client.Del(ctx, scopedKeys...).Err()
}

// SetNX sets a value only if the key does not already exist. Returns true if the key was set.
func (r *RedisEngine) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	byteValue, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	return r.client.SetNX(ctx, r.scopedKey(key), byteValue, ttl).Result()
}

// Expire sets a TTL on an existing key.
func (r *RedisEngine) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return r.client.Expire(ctx, r.scopedKey(key), ttl).Err()
}

// Increment increases key by one.
func (r *RedisEngine) Increment(ctx context.Context, key string) (int64, error) {
	return r.client.Incr(ctx, r.scopedKey(key)).Result()
}

// Decrement decreases key by one.
func (r *RedisEngine) Decrement(ctx context.Context, key string) (int64, error) {
	return r.client.Decr(ctx, r.scopedKey(key)).Result()
}

// InvalidatePrefix removes keys with the given prefix.
func (r *RedisEngine) InvalidatePrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return ErrInvalidConfig
	}
	keys, err := r.scan(ctx, r.scopedKey(prefix)+"*", maxScannedKeys)
	if err != nil {
		return err
	}
	for start := 0; start < len(keys); start += deleteBatchSize {
		end := min(start+deleteBatchSize, len(keys))
		if err := r.client.Unlink(ctx, keys[start:end]...).Err(); err != nil {
			return err
		}
	}
	return nil
}

// Keys returns a bounded SCAN result for compatibility.
func (r *RedisEngine) Keys(ctx context.Context, pattern string) ([]string, error) {
	if pattern == "" {
		return nil, ErrInvalidConfig
	}
	return r.scan(ctx, r.scopedKey(pattern), maxScannedKeys)
}

func (r *RedisEngine) scopedKey(key string) string {
	if r.namespace == "" {
		return key
	}
	return r.namespace + ":" + key
}

func (r *RedisEngine) scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	keys := make([]string, 0, min(scanCount, limit))
	var cursor uint64
	for {
		page, next, err := r.client.Scan(
			ctx,
			cursor,
			pattern,
			int64(min(scanCount, limit-len(keys))),
		).Result()
		if err != nil {
			return nil, err
		}
		if len(keys)+len(page) > limit {
			return nil, ErrScanLimit
		}
		keys = append(keys, page...)
		cursor = next
		if cursor == 0 {
			return keys, nil
		}
		if len(keys) >= limit {
			return nil, ErrScanLimit
		}
	}
}
