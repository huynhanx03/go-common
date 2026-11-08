package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	redisV9 "github.com/redis/go-redis/v9"

	"github.com/huynhanx03/go-common/pkg/common/cache"
)

func cacheGenerationKey(key string) string {
	tag := key
	if open := strings.IndexByte(key, '{'); open >= 0 {
		if close := strings.IndexByte(key[open+1:], '}'); close > 0 {
			tag = key[open+1 : open+1+close]
		}
	}
	return cacheGenerationPrefix + "{" + tag + "}:" + key
}

// SnapshotVersion returns the current persistent revision used to fence writes.
func (r *RedisEngine) SnapshotVersion(ctx context.Context, key string) (string, error) {
	if key == "" || len(key) > cache.MaxKeyBytes {
		return "", ErrInvalidConfig
	}
	key = r.scopedKey(key)
	version, err := r.runScopedScript(ctx, cacheSnapshotVersionScript, []string{cacheGenerationKey(key)}).Text()
	return version, mapCacheVersionError(err)
}

// SetWithVersion conditionally stores already encoded bytes.
func (r *RedisEngine) SetWithVersion(ctx context.Context, versionKey, storageKey string, value []byte, ttl time.Duration, token string) error {
	if versionKey == "" || storageKey == "" || token == "" || ttl <= 0 {
		return ErrInvalidConfig
	}
	versionKey = r.scopedKey(versionKey)
	storageKey = r.scopedKey(storageKey)
	ttlMillis := max(ttl.Milliseconds(), 1)
	result, err := r.runScopedScript(
		ctx,
		cacheSetIfVersionScript,
		[]string{cacheGenerationKey(versionKey), storageKey},
		token,
		value,
		ttlMillis,
	).Int64()
	if err != nil {
		return mapCacheVersionError(err)
	}
	return validateCacheWriteResult(result)
}

func validateCacheWriteResult(result int64) error {
	if result == 0 || result == 1 {
		return nil
	}
	return fmt.Errorf("%w: %d", ErrUnexpectedCacheWriteResult, result)
}

// DeleteValueWithVersion deletes a value only while its revision is current.
func (r *RedisEngine) DeleteValueWithVersion(ctx context.Context, versionKey, storageKey, token string) error {
	if versionKey == "" || storageKey == "" || token == "" {
		return ErrInvalidConfig
	}
	versionKey = r.scopedKey(versionKey)
	storageKey = r.scopedKey(storageKey)
	err := r.runScopedScript(
		ctx,
		cacheDeleteIfVersionScript,
		[]string{cacheGenerationKey(versionKey), storageKey},
		token,
	).Err()
	return mapCacheVersionError(err)
}

// Revoke advances the revision and removes the cached value and its negative entry.
func (r *RedisEngine) Revoke(ctx context.Context, key string) error {
	if key == "" || len(key) > cache.MaxKeyBytes {
		return ErrInvalidConfig
	}
	key = r.scopedKey(key)
	err := r.runScopedScript(
		ctx,
		cacheRevokeScript,
		[]string{cacheGenerationKey(key), key, key + cache.NegativeKeySuffix},
	).Err()
	return mapCacheVersionError(err)
}

// RevokeMany revokes keys in bounded pipelines, preserving per-key atomicity.
func (r *RedisEngine) RevokeMany(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if key == "" || len(key) > cache.MaxKeyBytes {
			return ErrInvalidConfig
		}
	}

	var errs []error
	for start := 0; start < len(keys); start += cacheRevokeBatchSize {
		end := min(start+cacheRevokeBatchSize, len(keys))
		if err := r.revokeBatch(ctx, keys[start:end]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *RedisEngine) revokeBatch(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if len(keys) == 1 {
		return r.Revoke(ctx, keys[0])
	}

	commands := make([]*redisV9.Cmd, 0, len(keys))
	_, pipelineErr := r.client.Pipelined(ctx, func(pipeline redisV9.Pipeliner) error {
		for _, key := range keys {
			key = r.scopedKey(key)
			redisKeys := []string{cacheGenerationKey(key), key, key + cache.NegativeKeySuffix}
			commands = append(commands, r.queueScopedScript(ctx, pipeline, cacheRevokeScript, redisKeys))
		}
		return nil
	})

	var errs []error
	for index, command := range commands {
		if err := command.Err(); err != nil {
			errs = append(errs, fmt.Errorf("revoke cache key %q: %w", keys[index], mapCacheVersionError(err)))
		}
	}
	if pipelineErr != nil && len(errs) == 0 {
		errs = append(errs, mapCacheVersionError(pipelineErr))
	}
	return errors.Join(errs...)
}
