//go:build integration

package redis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	redisV9 "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/logger"
)

func testCacheRevision(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Helper()

	const key = "redis-test:cache:revision"
	value := []byte{0x00, 0x01, 0xfe, 0xff}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := engine.SetWithVersion(cancelled, key, key, value, time.Minute, "0"); err == nil {
		t.Fatal("SetWithVersion with a cancelled context should fail")
	}
	version, err := engine.SnapshotVersion(ctx, key)
	if err != nil || version != "0" {
		t.Fatalf("initial SnapshotVersion = %q, %v; want 0", version, err)
	}
	if err := engine.SetWithVersion(ctx, key, key, value, time.Minute, version); err != nil {
		t.Fatalf("SetWithVersion: %v", err)
	}
	got, found, err := engine.Get(ctx, key)
	if err != nil || !found || !bytes.Equal(got, value) {
		t.Fatalf("Get raw value = %v, %v, %v; want %v", got, found, err, value)
	}

	if err := engine.DeleteValueWithVersion(ctx, key, key, "stale"); err != nil {
		t.Fatalf("DeleteValueWithVersion(stale): %v", err)
	}
	if _, found, err := engine.Get(ctx, key); err != nil || !found {
		t.Fatalf("stale conditional delete: found %v, error %v; want value retained", found, err)
	}
	if err := engine.DeleteValueWithVersion(ctx, key, key, version); err != nil {
		t.Fatalf("DeleteValueWithVersion(current): %v", err)
	}
	if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get after conditional delete = found %v, error %v; want missing key", found, err)
	}

	if err := engine.SetWithVersion(ctx, key, key, value, time.Minute, version); err != nil {
		t.Fatalf("SetWithVersion before revoke: %v", err)
	}
	if err := engine.Set(ctx, key+cache.NegativeKeySuffix, "negative", time.Minute); err != nil {
		t.Fatalf("Set negative entry: %v", err)
	}
	if err := engine.Revoke(ctx, key); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	version, err = engine.SnapshotVersion(ctx, key)
	if err != nil || version != "1" {
		t.Fatalf("SnapshotVersion after revoke = %q, %v; want 1", version, err)
	}
	if err := engine.SetWithVersion(ctx, key, key, []byte("stale"), time.Minute, "0"); err != nil {
		t.Fatalf("stale SetWithVersion: %v", err)
	}
	if err := engine.SetWithVersion(ctx, key, key+cache.NegativeKeySuffix, []byte("stale-negative"), time.Minute, "0"); err != nil {
		t.Fatalf("stale negative SetWithVersion: %v", err)
	}
	if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("stale write after revoke = found %v, error %v; want missing key", found, err)
	}
	if _, found, err := engine.Get(ctx, key+cache.NegativeKeySuffix); found || !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("negative entry after revoke = found %v, error %v; want missing key", found, err)
	}
	if err := engine.Revoke(ctx, key); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	if version, err = engine.SnapshotVersion(ctx, key); err != nil || version != "2" {
		t.Fatalf("SnapshotVersion after second revoke = %q, %v; want 2", version, err)
	}
}

func testCacheBatchRevocation(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Helper()

	keys := []string{
		"redis-test:cache:batch:first",
		"redis-test:cache:batch:second",
	}
	versions := make(map[string]string, len(keys))
	for _, key := range keys {
		version, err := engine.SnapshotVersion(ctx, key)
		if err != nil {
			t.Fatalf("SnapshotVersion(%q): %v", key, err)
		}
		versions[key] = version
		if err := engine.SetWithVersion(ctx, key, key, []byte("value"), time.Minute, version); err != nil {
			t.Fatalf("SetWithVersion(%q): %v", key, err)
		}
		if err := engine.Set(ctx, key+cache.NegativeKeySuffix, "negative", time.Minute); err != nil {
			t.Fatalf("Set negative entry for %q: %v", key, err)
		}
	}

	if err := engine.RevokeMany(ctx, keys...); err != nil {
		t.Fatalf("RevokeMany: %v", err)
	}
	for _, key := range keys {
		version, err := engine.SnapshotVersion(ctx, key)
		if err != nil || version == versions[key] {
			t.Fatalf("SnapshotVersion(%q) after batch revoke = %q, %v; want advanced version", key, version, err)
		}
		if err := engine.SetWithVersion(ctx, key, key, []byte("stale"), time.Minute, versions[key]); err != nil {
			t.Fatalf("stale SetWithVersion(%q): %v", key, err)
		}
		if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Get(%q) after batch revoke = found %v, error %v; want missing", key, found, err)
		}
		if _, found, err := engine.Get(ctx, key+cache.NegativeKeySuffix); found || !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Get negative entry for %q = found %v, error %v; want missing", key, found, err)
		}
	}
}

func testCacheVersionIntegrity(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Helper()

	malformedVersions := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "text", value: "invalid"},
		{name: "negative", value: "-1"},
		{name: "leading zero", value: "01"},
		{name: "above int64", value: "9223372036854775808"},
	}
	operations := []struct {
		name string
		call func(context.Context, *RedisEngine, string) error
	}{
		{name: "snapshot", call: func(ctx context.Context, engine *RedisEngine, key string) error {
			_, err := engine.SnapshotVersion(ctx, key)
			return err
		}},
		{name: "conditional set", call: func(ctx context.Context, engine *RedisEngine, key string) error {
			return engine.SetWithVersion(ctx, key, key, []byte("replacement"), time.Minute, "0")
		}},
		{name: "conditional delete", call: func(ctx context.Context, engine *RedisEngine, key string) error {
			return engine.DeleteValueWithVersion(ctx, key, key, "0")
		}},
		{name: "revoke", call: func(ctx context.Context, engine *RedisEngine, key string) error {
			return engine.Revoke(ctx, key)
		}},
	}
	for _, version := range malformedVersions {
		t.Run(version.name, func(t *testing.T) {
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					key := fmt.Sprintf("redis-test:cache:corrupt:%s:%s", version.name, operation.name)
					generationKey := cacheGenerationKey(engine.scopedKey(key))
					if err := engine.client.Set(ctx, generationKey, version.value, 0).Err(); err != nil {
						t.Fatalf("seed generation: %v", err)
					}
					if err := engine.client.Set(ctx, engine.scopedKey(key), "positive", time.Minute).Err(); err != nil {
						t.Fatalf("seed positive value: %v", err)
					}
					if err := engine.client.Set(ctx, engine.scopedKey(key+cache.NegativeKeySuffix), "negative", time.Minute).Err(); err != nil {
						t.Fatalf("seed negative value: %v", err)
					}

					if err := operation.call(ctx, engine, key); err == nil {
						t.Fatal("operation accepted corrupt generation metadata")
					} else {
						if !errors.Is(err, ErrInvalidCacheVersion) {
							t.Errorf("operation error = %v; want ErrInvalidCacheVersion", err)
						}
						var redisErr redisV9.Error
						if !errors.As(err, &redisErr) {
							t.Errorf("operation error = %T %v; want native Redis cause", err, err)
						}
					}
					assertRedisValue(t, ctx, engine, generationKey, version.value)
					assertRedisValue(t, ctx, engine, engine.scopedKey(key), "positive")
					assertRedisValue(t, ctx, engine, engine.scopedKey(key+cache.NegativeKeySuffix), "negative")
				})
			}
		})
	}

	t.Run("revoke at int64 maximum preserves values", func(t *testing.T) {
		key := "redis-test:cache:version-overflow"
		generationKey := cacheGenerationKey(engine.scopedKey(key))
		seedCacheValues(t, ctx, engine, key, "9223372036854775807")
		if err := engine.Revoke(ctx, key); err == nil {
			t.Fatal("Revoke accepted an exhausted generation")
		}
		assertRedisValue(t, ctx, engine, generationKey, "9223372036854775807")
		assertRedisValue(t, ctx, engine, engine.scopedKey(key), "positive")
		assertRedisValue(t, ctx, engine, engine.scopedKey(key+cache.NegativeKeySuffix), "negative")
	})

	t.Run("revoke increments exact decimal above float precision", func(t *testing.T) {
		key := "redis-test:cache:version-precision"
		generationKey := cacheGenerationKey(engine.scopedKey(key))
		if err := engine.client.Set(ctx, generationKey, "9007199254740992", 0).Err(); err != nil {
			t.Fatalf("seed generation: %v", err)
		}
		if version, err := engine.SnapshotVersion(ctx, key); err != nil || version != "9007199254740992" {
			t.Fatalf("SnapshotVersion = %q, %v; want exact decimal", version, err)
		}
		if err := engine.Revoke(ctx, key); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if version, err := engine.SnapshotVersion(ctx, key); err != nil || version != "9007199254740993" {
			t.Fatalf("SnapshotVersion after revoke = %q, %v; want exact increment", version, err)
		}
	})

	t.Run("batch revoke isolates corrupt key", func(t *testing.T) {
		validKey := "redis-test:cache:batch:valid-with-corrupt"
		corruptKey := "redis-test:cache:batch:corrupt"
		seedCacheValues(t, ctx, engine, validKey, "0")
		seedCacheValues(t, ctx, engine, corruptKey, "01")
		err := engine.RevokeMany(ctx, validKey, corruptKey)
		if err == nil {
			t.Fatal("RevokeMany returned nil for corrupt generation")
		}
		if !errors.Is(err, ErrInvalidCacheVersion) {
			t.Fatalf("RevokeMany error = %v; want ErrInvalidCacheVersion", err)
		}
		if !strings.Contains(err.Error(), corruptKey) {
			t.Fatalf("RevokeMany error = %v; want failing key %q identified", err, corruptKey)
		}
		assertRedisMissing(t, ctx, engine, engine.scopedKey(validKey))
		assertRedisMissing(t, ctx, engine, engine.scopedKey(validKey+cache.NegativeKeySuffix))
		assertRedisValue(t, ctx, engine, engine.scopedKey(corruptKey), "positive")
		assertRedisValue(t, ctx, engine, engine.scopedKey(corruptKey+cache.NegativeKeySuffix), "negative")
	})

	t.Run("Fetch returns loader value and skips writes on corrupt generation", func(t *testing.T) {
		key := "redis-test:cache:fetch-corrupt-version"
		generationKey := cacheGenerationKey(engine.scopedKey(key))
		if err := engine.client.Set(ctx, generationKey, "broken", 0).Err(); err != nil {
			t.Fatalf("seed generation: %v", err)
		}
		core, observed := observer.New(zap.ErrorLevel)
		c, err := cache.New(cache.Config{L2: engine, Logger: logger.Wrap(zap.New(core))})
		if err != nil {
			t.Fatalf("create cache: %v", err)
		}
		got, err := cache.Fetch[string](ctx, c, key, time.Minute, func(context.Context) (string, error) {
			return "loaded", nil
		})
		if err != nil || got != "loaded" {
			t.Fatalf("Fetch = %q, %v; want loader result", got, err)
		}
		if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("cached value = found %v, error %v; want no write", found, err)
		}
		entries := observed.FilterMessage("cache operation failed").All()
		if len(entries) != 1 || entries[0].ContextMap()["operation"] != "l2_snapshot_version" {
			t.Fatalf("cache operation logs = %#v; want one version snapshot error", entries)
		}
		loggedError, ok := entries[0].ContextMap()["error"].(string)
		if !ok || !strings.Contains(loggedError, ErrInvalidCacheVersion.Error()) {
			t.Fatalf("logged cache error = %#v; want invalid cache generation", entries[0].ContextMap()["error"])
		}
	})
}

func seedCacheValues(t *testing.T, ctx context.Context, engine *RedisEngine, key, version string) {
	t.Helper()
	if err := engine.client.Set(ctx, cacheGenerationKey(engine.scopedKey(key)), version, 0).Err(); err != nil {
		t.Fatalf("seed generation: %v", err)
	}
	if err := engine.client.Set(ctx, engine.scopedKey(key), "positive", time.Minute).Err(); err != nil {
		t.Fatalf("seed positive value: %v", err)
	}
	if err := engine.client.Set(ctx, engine.scopedKey(key+cache.NegativeKeySuffix), "negative", time.Minute).Err(); err != nil {
		t.Fatalf("seed negative value: %v", err)
	}
}

func assertRedisValue(t *testing.T, ctx context.Context, engine *RedisEngine, key, want string) {
	t.Helper()
	value, err := engine.client.Get(ctx, key).Result()
	if err != nil || value != want {
		t.Fatalf("GET(%q) = %q, %v; want %q", key, value, err, want)
	}
}

func assertRedisMissing(t *testing.T, ctx context.Context, engine *RedisEngine, key string) {
	t.Helper()
	if err := engine.client.Get(ctx, key).Err(); !errors.Is(err, redisV9.Nil) {
		t.Fatalf("GET(%q) error = %v; want missing key", key, err)
	}
}
