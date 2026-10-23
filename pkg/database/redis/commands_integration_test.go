//go:build integration

package redis

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func testRedisCommands(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Helper()

	t.Run("values and namespace", func(t *testing.T) {
		const key = "redis-test:value"
		if err := engine.Set(ctx, key, "first", time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if exists, err := engine.Client().Exists(ctx, "redis-test-app:"+key).Result(); err != nil || exists != 1 {
			t.Fatalf("namespaced key exists = %d, %v; want 1", exists, err)
		}
		if exists, err := engine.Client().Exists(ctx, key).Result(); err != nil || exists != 0 {
			t.Fatalf("unscoped key exists = %d, %v; want 0", exists, err)
		}
		if err := engine.Set(ctx, key, "updated", time.Minute); err != nil {
			t.Fatalf("overwrite Set: %v", err)
		}
		got, found, err := engine.Get(ctx, key)
		if err != nil || !found || string(got) != `"updated"` {
			t.Fatalf("Get = %q, %v, %v; want JSON string updated", got, found, err)
		}
		if set, err := engine.SetNX(ctx, key, "ignored", time.Minute); err != nil || set {
			t.Fatalf("SetNX existing: %v", err)
		} else if set, err := engine.SetNX(ctx, key+":new", "created", time.Minute); err != nil || !set {
			t.Fatal("SetNX should set a missing key")
		}
		if err := engine.Expire(ctx, key, 2*time.Minute); err != nil {
			t.Fatalf("Expire: %v", err)
		}
		if err := engine.Delete(ctx, key); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Get after Delete = found %v, error %v; want missing key", found, err)
		}
	})

	t.Run("batches", func(t *testing.T) {
		keys := []string{"redis-test:batch:1", "redis-test:batch:2"}
		if err := engine.BatchSet(ctx, map[string]any{keys[0]: 1, keys[1]: 2}, time.Minute); err != nil {
			t.Fatalf("BatchSet: %v", err)
		}
		if err := engine.BatchSet(ctx, map[string]any{"redis-test:batch:invalid": make(chan int)}, time.Minute); err == nil {
			t.Fatal("BatchSet with an unencodable value should fail")
		}
		if err := engine.DeleteBulk(ctx, nil); err != nil {
			t.Fatalf("DeleteBulk(empty): %v", err)
		}
		if err := engine.DeleteBulk(ctx, keys); err != nil {
			t.Fatalf("DeleteBulk: %v", err)
		}
		for _, key := range keys {
			if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
				t.Errorf("Get(%q) after DeleteBulk = found %v, error %v; want missing", key, found, err)
			}
		}
	})

	t.Run("counters and key scans", func(t *testing.T) {
		const counter = "redis-test:counter"
		for _, want := range []int64{1, 2} {
			got, err := engine.Increment(ctx, counter)
			if err != nil || got != want {
				t.Fatalf("Increment = %d, %v; want %d", got, err, want)
			}
		}
		if got, err := engine.Decrement(ctx, counter); err != nil || got != 1 {
			t.Fatalf("Decrement = %d, %v; want 1", got, err)
		}

		const prefix = "redis-test:prefix:"
		for _, key := range []string{prefix + "one", prefix + "two", "redis-test:other"} {
			if err := engine.Set(ctx, key, key, 0); err != nil {
				t.Fatalf("Set(%q): %v", key, err)
			}
		}
		keys, err := engine.Keys(ctx, "redis-test:prefix:*one")
		if err != nil || len(keys) != 1 {
			t.Fatalf("Keys = %v, %v; want one matching key", keys, err)
		}
		if err := engine.InvalidatePrefix(ctx, prefix); err != nil {
			t.Fatalf("InvalidatePrefix: %v", err)
		}
		if _, found, err := engine.Get(ctx, "redis-test:other"); err != nil || !found {
			t.Fatalf("unrelated key after invalidation: found %v, error %v", found, err)
		}
		if _, err := engine.Keys(ctx, ""); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("Keys(empty pattern) error = %v; want ErrInvalidConfig", err)
		}
		if err := engine.InvalidatePrefix(ctx, ""); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("InvalidatePrefix(empty) error = %v; want ErrInvalidConfig", err)
		}

		const oversizedPageFixtureCount = 32
		oversizedPageValues := make(map[string]any, oversizedPageFixtureCount)
		oversizedPageKeys := make([]string, 0, oversizedPageFixtureCount)
		for i := 0; i < oversizedPageFixtureCount; i++ {
			key := fmt.Sprintf("redis-test:scan-page:%02d", i)
			oversizedPageValues[key] = i
			oversizedPageKeys = append(oversizedPageKeys, key)
		}
		if err := engine.BatchSet(ctx, oversizedPageValues, 0); err != nil {
			t.Fatalf("BatchSet scan page fixtures: %v", err)
		}
		if _, err := engine.scan(ctx, engine.scopedKey("redis-test:scan-page:*"), 1); !errors.Is(err, ErrScanLimit) {
			t.Fatalf("scan whose page exceeds its limit error = %v; want ErrScanLimit", err)
		}
		if err := engine.DeleteBulk(ctx, oversizedPageKeys); err != nil {
			t.Fatalf("DeleteBulk scan page fixtures: %v", err)
		}

		const limitFixtureCount = 2048
		limitValues := make(map[string]any, limitFixtureCount)
		limitKeys := make([]string, 0, limitFixtureCount)
		for i := 0; i < limitFixtureCount; i++ {
			key := fmt.Sprintf("redis-test:scan-limit:%02d", i)
			limitValues[key] = i
			limitKeys = append(limitKeys, key)
		}
		if err := engine.BatchSet(ctx, limitValues, 0); err != nil {
			t.Fatalf("BatchSet scan limit fixtures: %v", err)
		}
		// A unique match ensures the returned page cannot exceed the limit. Try
		// several keys so at least one match is found before SCAN reaches cursor 0.
		cursorLimitReached := false
		for _, key := range limitKeys[:32] {
			_, err := engine.scan(ctx, engine.scopedKey(key), 1)
			if errors.Is(err, ErrScanLimit) {
				cursorLimitReached = true
				break
			}
			if err != nil {
				t.Fatalf("scan exact key %q: %v", key, err)
			}
		}
		if !cursorLimitReached {
			t.Fatal("scan did not report the limit while more cursor pages remained")
		}
		if err := engine.DeleteBulk(ctx, limitKeys); err != nil {
			t.Fatalf("DeleteBulk scan limit fixtures: %v", err)
		}
	})

	t.Run("command errors", func(t *testing.T) {
		if err := engine.Set(ctx, "redis-test:integer", "not-an-integer", time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Increment(ctx, "redis-test:integer"); err == nil {
			t.Fatal("Increment on non-integer value should fail")
		}
		if err := engine.Set(ctx, "redis-test:wrong-type", "value", time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.ZCount(ctx, "redis-test:wrong-type", "-inf", "+inf"); err == nil {
			t.Fatal("ZCount on a string should fail")
		}
		if err := engine.Set(ctx, "redis-test:unencodable", make(chan int), time.Minute); err == nil {
			t.Fatal("Set with an unencodable value should fail")
		}

		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := engine.Get(cancelled, "redis-test:cancelled"); err == nil {
			t.Fatal("Get with a cancelled context should fail")
		}
		if err := engine.Set(cancelled, "redis-test:cancelled", "value", time.Minute); err == nil {
			t.Fatal("Set with a cancelled context should fail")
		}
		if err := engine.InvalidatePrefix(cancelled, "redis-test:cancelled:"); err == nil {
			t.Fatal("InvalidatePrefix with a cancelled context should fail")
		}
	})

	t.Run("invalidation reports unlink errors", func(t *testing.T) {
		const prefix = "redis-test:unlink-error:"
		if err := engine.Set(ctx, prefix+"key", "value", 0); err != nil {
			t.Fatalf("Set invalidation fixture: %v", err)
		}
		client := engine.Client()
		if err := client.ACLSetUser(ctx, "default", "on", "nopass", "~*", "&*", "+@all", "-unlink").Err(); err != nil {
			t.Fatalf("disable UNLINK for default user: %v", err)
		}
		defer func() {
			if err := client.ACLSetUser(ctx, "default", "on", "nopass", "~*", "&*", "+@all").Err(); err != nil {
				t.Errorf("restore default user permissions: %v", err)
			}
		}()

		if err := engine.InvalidatePrefix(ctx, prefix); err == nil {
			t.Fatal("InvalidatePrefix with UNLINK denied should fail")
		}
	})

}
