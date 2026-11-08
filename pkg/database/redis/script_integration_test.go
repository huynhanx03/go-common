//go:build integration

package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	redisV9 "github.com/redis/go-redis/v9"
)

var idempotentCounterScript = redisV9.NewScript(`
local amount = tonumber(ARGV[1])
local retention = tonumber(ARGV[2])
if not amount or amount < 1 or amount ~= math.floor(amount) then
  return redis.error_reply('invalid amount')
end
if not retention or retention < 1 or retention ~= math.floor(retention) then
  return redis.error_reply('invalid marker retention')
end

local counter_type = redis.call('TYPE', KEYS[1]).ok
if counter_type ~= 'none' and counter_type ~= 'string' then
  return redis.error_reply('counter has the wrong type')
end
if redis.call('EXISTS', KEYS[2]) == 1 then
  return {0, tonumber(redis.call('GET', KEYS[1]) or '0')}
end

local count = redis.call('INCRBY', KEYS[1], amount)
redis.call('SET', KEYS[2], '1', 'PX', retention)
return {1, count}
`)

func testRedisScripts(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Run("documented usage", func(t *testing.T) {
		counter, marker := scriptKeys(t)
		applied, count := applyCounterScript(t, ctx, engine, counter, marker, "4", "5000")
		if !applied || count != 4 {
			t.Fatalf("RunScript() result = (%t, %d); want (true, 4)", applied, count)
		}
	})

	t.Run("deduplicates a repeated operation", func(t *testing.T) {
		counter, marker := scriptKeys(t)
		first, count := applyCounterScript(t, ctx, engine, counter, marker, "1", "5000")
		if !first || count != 1 {
			t.Fatalf("first result = (%t, %d); want (true, 1)", first, count)
		}

		second, count := applyCounterScript(t, ctx, engine, counter, marker, "1", "5000")
		if second || count != 1 {
			t.Fatalf("duplicate result = (%t, %d); want (false, 1)", second, count)
		}
		assertStoredScriptValues(t, ctx, engine, counter, marker, "1", "1")
	})

	t.Run("concurrent same operation increments once", func(t *testing.T) {
		counter, marker := scriptKeys(t)
		var applied int
		var mu sync.Mutex
		runCounterConcurrently(t, 64, func() {
			wasApplied, _, err := executeCounterScript(ctx, engine, counter, marker, "1", "5000")
			if err != nil {
				t.Errorf("RunScript() error = %v", err)
				return
			}
			if wasApplied {
				mu.Lock()
				applied++
				mu.Unlock()
			}
		})
		if applied != 1 {
			t.Fatalf("applied operations = %d; want 1", applied)
		}
		assertStoredScriptValues(t, ctx, engine, counter, marker, "1", "1")
	})

	t.Run("concurrent distinct operations retain every increment", func(t *testing.T) {
		counter, _ := scriptKeys(t)
		var wg sync.WaitGroup
		errs := make(chan error, 64)
		for i := range 64 {
			marker := fmt.Sprintf("%s:operation-%d", counter, i)
			wg.Add(1)
			go func() {
				defer wg.Done()
				applied, _, err := executeCounterScript(ctx, engine, counter, marker, "1", "5000")
				if err != nil {
					errs <- err
				} else if !applied {
					errs <- errors.New("distinct operation was reported as a duplicate")
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		assertStoredScriptValues(t, ctx, engine, counter, "", "64", "")
	})

	t.Run("validates before writing the marker", func(t *testing.T) {
		t.Run("invalid amount", func(t *testing.T) {
			counter, marker := scriptKeys(t)
			if _, _, err := executeCounterScript(ctx, engine, counter, marker, "0", "5000"); err == nil {
				t.Fatal("RunScript() accepted a zero increment")
			}
			assertKeyMissing(t, ctx, engine, marker)
		})

		t.Run("wrong counter type", func(t *testing.T) {
			counter, marker := scriptKeys(t)
			if err := engine.client.LPush(ctx, engine.scopedKey(counter), "not-a-counter").Err(); err != nil {
				t.Fatalf("LPush() error = %v", err)
			}
			if _, _, err := executeCounterScript(ctx, engine, counter, marker, "1", "5000"); err == nil {
				t.Fatal("RunScript() accepted a list as a counter")
			}
			assertKeyMissing(t, ctx, engine, marker)
		})
	})

	t.Run("reloads a flushed script and uses the final namespaced keys", func(t *testing.T) {
		counter, marker := scriptKeys(t)
		if applied, count := applyCounterScript(t, ctx, engine, counter, marker, "2", "5000"); !applied || count != 2 {
			t.Fatalf("initial script result = (%t, %d); want (true, 2)", applied, count)
		}
		if err := engine.client.ScriptFlush(ctx).Err(); err != nil {
			t.Fatalf("SCRIPT FLUSH error = %v", err)
		}
		if applied, count := applyCounterScript(t, ctx, engine, counter, marker+":after-flush", "3", "5000"); !applied || count != 5 {
			t.Fatalf("script result after flush = (%t, %d); want (true, 5)", applied, count)
		}
		assertStoredScriptValues(t, ctx, engine, counter, marker, "5", "1")
	})

	t.Run("isolates identical logical keys by namespace", func(t *testing.T) {
		counter, marker := scriptKeys(t)
		other := &RedisEngine{client: engine.client, namespace: "redis-script-other"}
		if applied, _ := applyCounterScript(t, ctx, engine, counter, marker, "1", "5000"); !applied {
			t.Fatal("first namespace did not apply operation")
		}
		if applied, _ := applyCounterScript(t, ctx, other, counter, marker, "1", "5000"); !applied {
			t.Fatal("second namespace did not apply operation")
		}
		assertStoredScriptValues(t, ctx, engine, counter, marker, "1", "1")
		assertStoredScriptValues(t, ctx, other, counter, marker, "1", "1")
	})

	t.Run("returns Redis script errors as native Redis errors", func(t *testing.T) {
		script := redisV9.NewScript(`return redis.error_reply('expected failure')`)
		_, err := engine.RunScript(ctx, script, []string{scriptKey(t, "error")})
		var redisErr redisV9.Error
		if !errors.As(err, &redisErr) {
			t.Fatalf("RunScript() error = %T %v; want native Redis error", err, err)
		}
	})

	t.Run("marker retention bounds deduplication", func(t *testing.T) {
		counter, marker := scriptKeys(t)
		if applied, count := applyCounterScript(t, ctx, engine, counter, marker, "1", "100"); !applied || count != 1 {
			t.Fatalf("initial result = (%t, %d); want (true, 1)", applied, count)
		}
		waitForScriptKeyExpiry(t, ctx, engine, marker, time.Second)
		if applied, count := applyCounterScript(t, ctx, engine, counter, marker, "1", "5000"); !applied || count != 2 {
			t.Fatalf("result after marker expiry = (%t, %d); want (true, 2)", applied, count)
		}
	})
}

func TestRunScriptRedisCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	clusterClient := containers.StartRedisCluster(t)
	engine := &RedisEngine{client: clusterClient, namespace: "redis-script-cluster"}

	t.Run("keys in one final hash slot execute atomically", func(t *testing.T) {
		counter := "scripts:counter:{resource-42}"
		marker := "scripts:marker:{resource-42}:operation-1"
		counterSlot, err := clusterClient.ClusterKeySlot(ctx, engine.scopedKey(counter)).Result()
		if err != nil {
			t.Fatalf("CLUSTER KEYSLOT counter error = %v", err)
		}
		markerSlot, err := clusterClient.ClusterKeySlot(ctx, engine.scopedKey(marker)).Result()
		if err != nil {
			t.Fatalf("CLUSTER KEYSLOT marker error = %v", err)
		}
		if counterSlot != markerSlot {
			t.Fatalf("final keys use slots %d and %d; want one shared slot", counterSlot, markerSlot)
		}

		if applied, count := applyCounterScript(t, ctx, engine, counter, marker, "3", "5000"); !applied || count != 3 {
			t.Fatalf("RunScript() result = (%t, %d); want (true, 3)", applied, count)
		}
		assertStoredScriptValues(t, ctx, engine, counter, marker, "3", "1")
	})

	t.Run("cache generations preserve one slot and reject corrupt metadata", func(t *testing.T) {
		key := "scripts:cache:{resource-42}"
		version, err := engine.SnapshotVersion(ctx, key)
		if err != nil || version != "0" {
			t.Fatalf("initial version = %q, %v; want 0", version, err)
		}
		if err := engine.SetWithVersion(ctx, key, key, []byte("positive"), time.Minute, version); err != nil {
			t.Fatalf("SetWithVersion: %v", err)
		}
		if err := engine.Revoke(ctx, key); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if version, err := engine.SnapshotVersion(ctx, key); err != nil || version != "1" {
			t.Fatalf("version after revoke = %q, %v; want 1", version, err)
		}

		generationKey := cacheGenerationKey(engine.scopedKey(key))
		for _, cacheKey := range []string{key, key + cache.NegativeKeySuffix} {
			if err := clusterClient.Set(ctx, engine.scopedKey(cacheKey), "preserved", 0).Err(); err != nil {
				t.Fatalf("seed cache value: %v", err)
			}
		}
		if err := clusterClient.Set(ctx, generationKey, "01", 0).Err(); err != nil {
			t.Fatalf("seed corrupt generation: %v", err)
		}
		if _, err := engine.SnapshotVersion(ctx, key); !errors.Is(err, ErrInvalidCacheVersion) {
			t.Fatalf("SnapshotVersion error = %v; want invalid generation", err)
		}
		if err := engine.SetWithVersion(ctx, key, key, []byte("overwrite"), time.Minute, "1"); !errors.Is(err, ErrInvalidCacheVersion) {
			t.Fatalf("SetWithVersion error = %v; want invalid generation", err)
		}
		if err := engine.DeleteValueWithVersion(ctx, key, key, "1"); !errors.Is(err, ErrInvalidCacheVersion) {
			t.Fatalf("DeleteValueWithVersion error = %v; want invalid generation", err)
		}
		if err := engine.Revoke(ctx, key); !errors.Is(err, ErrInvalidCacheVersion) {
			t.Fatalf("Revoke error = %v; want invalid generation", err)
		}
		assertRedisValue(t, ctx, engine, generationKey, "01")
		assertRedisValue(t, ctx, engine, engine.scopedKey(key), "preserved")
		assertRedisValue(t, ctx, engine, engine.scopedKey(key+cache.NegativeKeySuffix), "preserved")
	})

	t.Run("different final hash slots preserve CROSSSLOT and do not write", func(t *testing.T) {
		counter := "scripts:counter:{resource-left}"
		marker := "scripts:marker:{resource-right}:operation-2"
		counterKey := engine.scopedKey(counter)
		markerKey := engine.scopedKey(marker)
		counterSlot, err := clusterClient.ClusterKeySlot(ctx, counterKey).Result()
		if err != nil {
			t.Fatalf("CLUSTER KEYSLOT counter error = %v", err)
		}
		markerSlot, err := clusterClient.ClusterKeySlot(ctx, markerKey).Result()
		if err != nil {
			t.Fatalf("CLUSTER KEYSLOT marker error = %v", err)
		}
		if counterSlot == markerSlot {
			t.Fatalf("test keys unexpectedly share slot %d", counterSlot)
		}
		if err := clusterClient.Set(ctx, counterKey, "10", 0).Err(); err != nil {
			t.Fatalf("SET counter error = %v", err)
		}
		if err := clusterClient.Set(ctx, markerKey, "untouched", 0).Err(); err != nil {
			t.Fatalf("SET marker error = %v", err)
		}

		_, err = engine.RunScript(ctx, idempotentCounterScript, []string{counter, marker}, "2", "5000")
		if err == nil || !strings.Contains(strings.ToUpper(err.Error()), "CROSSSLOT") {
			t.Fatalf("RunScript() error = %v; want native CROSSSLOT", err)
		}
		assertStoredScriptValues(t, ctx, engine, counter, marker, "10", "untouched")
	})
}

func scriptKeys(t *testing.T) (string, string) {
	t.Helper()
	key := strings.NewReplacer("/", ":", " ", "-").Replace(t.Name())
	return "script:" + key + ":counter", "script:" + key + ":marker"
}

func scriptKey(t *testing.T, suffix string) string {
	t.Helper()
	return "script:" + strings.NewReplacer("/", ":", " ", "-").Replace(t.Name()) + ":" + suffix
}

func applyCounterScript(t *testing.T, ctx context.Context, engine *RedisEngine, counter, marker, amount, retention string) (bool, int64) {
	t.Helper()
	applied, count, err := executeCounterScript(ctx, engine, counter, marker, amount, retention)
	if err != nil {
		t.Fatalf("RunScript() error = %v", err)
	}
	return applied, count
}

func executeCounterScript(ctx context.Context, engine *RedisEngine, counter, marker, amount, retention string) (bool, int64, error) {
	result, err := engine.RunScript(ctx, idempotentCounterScript, []string{counter, marker}, amount, retention)
	if err != nil {
		return false, 0, err
	}
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return false, 0, fmt.Errorf("unexpected script result %T", result)
	}
	status, statusOK := values[0].(int64)
	count, countOK := values[1].(int64)
	if !statusOK || !countOK {
		return false, 0, fmt.Errorf("unexpected script result values %#v", values)
	}
	return status == 1, count, nil
}

func runCounterConcurrently(t *testing.T, count int, run func()) {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run()
		}()
	}
	close(start)
	wg.Wait()
}

func assertStoredScriptValues(t *testing.T, ctx context.Context, engine *RedisEngine, counter, marker, wantCounter, wantMarker string) {
	t.Helper()
	if counter != "" {
		got, err := engine.client.Get(ctx, engine.scopedKey(counter)).Result()
		if err != nil || got != wantCounter {
			t.Fatalf("stored counter = %q, %v; want %q", got, err, wantCounter)
		}
	}
	if marker != "" {
		got, err := engine.client.Get(ctx, engine.scopedKey(marker)).Result()
		if err != nil || got != wantMarker {
			t.Fatalf("stored marker = %q, %v; want %q", got, err, wantMarker)
		}
	}
}

func assertKeyMissing(t *testing.T, ctx context.Context, engine *RedisEngine, key string) {
	t.Helper()
	count, err := engine.client.Exists(ctx, engine.scopedKey(key)).Result()
	if err != nil || count != 0 {
		t.Fatalf("key exists after rejected script: count=%d err=%v", count, err)
	}
}

func waitForScriptKeyExpiry(t *testing.T, ctx context.Context, engine *RedisEngine, key string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		count, err := engine.client.Exists(ctx, engine.scopedKey(key)).Result()
		if err != nil {
			t.Fatalf("EXISTS() error = %v", err)
		}
		if count == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("marker %q did not expire within %s", key, timeout)
}
