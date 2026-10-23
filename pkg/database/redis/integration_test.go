//go:build integration

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/testutil/containers"
)

func TestRedisIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	engine := newTestEngine(t)
	t.Run("commands", func(t *testing.T) { testRedisCommands(t, ctx, engine) })
	t.Run("geospatial and sorted sets", func(t *testing.T) { testRedisStructures(t, ctx, engine) })
	t.Run("cache revision fencing", func(t *testing.T) { testCacheRevision(t, ctx, engine) })
	t.Run("batch cache revocation", func(t *testing.T) { testCacheBatchRevocation(t, ctx, engine) })
}

func newTestEngine(t *testing.T) *RedisEngine {
	t.Helper()

	address := containers.StartRedis(t)
	config := testRedisConfig(address)
	config.Namespace = "redis-test-app"
	config.PoolSize = 10
	config.DialTimeout = 5
	config.ReadTimeout = 3
	config.WriteTimeout = 3
	config.PoolTimeout = 5
	config.MinRetryBackoff = 100
	config.MaxRetryBackoff = 500

	engine, err := NewConnection(config)
	if err != nil {
		t.Fatalf("connect to Redis: %v", err)
	}
	t.Cleanup(engine.Close)
	return engine
}
