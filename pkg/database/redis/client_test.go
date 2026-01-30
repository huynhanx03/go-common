package redis

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/huynhanx03/go-common/pkg/settings"
)

const (
	redisImage = "redis:7"
	redisPort  = "6379/tcp"
)

func TestClient_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	if !isDockerRunning(ctx) {
		t.Skip("Docker is not running, skipping integration test")
	}

	uri, terminate, err := setupRedisContainer(ctx)
	if err != nil {
		t.Fatalf("failed to setup redis container: %v", err)
	}
	defer terminate()

	cfg := &settings.Redis{
		Addrs:           []string{uri},
		Password:        "",
		Database:        0,
		PoolSize:        10,
		MinIdleConns:    1,
		DialTimeout:     5,
		ReadTimeout:     3,
		WriteTimeout:    3,
		PoolTimeout:     5,
		MaxRetries:      3,
		MinRetryBackoff: 100,
		MaxRetryBackoff: 500,
	}

	engine, err := NewConnection(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to redis: %v", err)
	}
	defer engine.Close()

	t.Run("Set", func(t *testing.T) {
		testSet(t, ctx, engine)
	})

	t.Run("Get", func(t *testing.T) {
		testGet(t, ctx, engine)
	})

	t.Run("Update", func(t *testing.T) {
		testUpdate(t, ctx, engine)
	})

	t.Run("Delete", func(t *testing.T) {
		testDelete(t, ctx, engine)
	})

	t.Run("InvalidatePrefix", func(t *testing.T) {
		testInvalidatePrefix(t, ctx, engine)
	})
}

func testSet(t *testing.T, ctx context.Context, engine *RedisEngine) {
	key := "test-set-key"
	value := map[string]string{"foo": "bar"}

	if err := engine.Set(ctx, key, value, 0); err != nil {
		t.Fatalf("Failed to set key: %v", err)
	}
}

func testGet(t *testing.T, ctx context.Context, engine *RedisEngine) {
	key := "test-get-key"
	value := "some-value"

	engine.Set(ctx, key, value, 0)

	bytes, found, err := engine.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get key: %v", err)
	}
	if !found {
		t.Fatal("Key should be found")
	}
	if string(bytes) != "\"some-value\"" {
		t.Errorf("Expected '\"some-value\"', got '%s'", string(bytes))
	}
}

func testUpdate(t *testing.T, ctx context.Context, engine *RedisEngine) {
	key := "test-update-key"

	// Initial Set
	if err := engine.Set(ctx, key, "value1", 0); err != nil {
		t.Fatalf("Failed to initial set key: %v", err)
	}

	// Update (Overwrite)
	if err := engine.Set(ctx, key, "value2", 0); err != nil {
		t.Fatalf("Failed to update key: %v", err)
	}

	bytes, found, _ := engine.Get(ctx, key)
	if !found {
		t.Fatal("Key should exist after update")
	}
	if string(bytes) != "\"value2\"" {
		t.Errorf("Expected '\"value2\"', got '%s'", string(bytes))
	}
}

func testDelete(t *testing.T, ctx context.Context, engine *RedisEngine) {
	key := "test-delete-key"
	engine.Set(ctx, key, "val", 0)

	if err := engine.Delete(ctx, key); err != nil {
		t.Fatalf("Failed to delete key: %v", err)
	}

	_, found, _ := engine.Get(ctx, key)
	if found {
		t.Error("Key should not exist after delete")
	}
}

func testInvalidatePrefix(t *testing.T, ctx context.Context, engine *RedisEngine) {
	prefix := "prefix-"
	engine.Set(ctx, prefix+"1", "v1", 0)
	engine.Set(ctx, prefix+"2", "v2", 0)
	engine.Set(ctx, "other-key", "v3", 0)

	if err := engine.InvalidatePrefix(ctx, prefix); err != nil {
		t.Fatalf("Failed to invalidate prefix: %v", err)
	}

	_, found1, _ := engine.Get(ctx, prefix+"1")
	_, found2, _ := engine.Get(ctx, prefix+"2")
	_, found3, _ := engine.Get(ctx, "other-key")

	if found1 || found2 {
		t.Error("Keys with prefix should be deleted")
	}
	if !found3 {
		t.Error("Key without prefix should remain")
	}
}

func setupRedisContainer(ctx context.Context) (string, func(), error) {
	req := testcontainers.ContainerRequest{
		Image:        redisImage,
		ExposedPorts: []string{redisPort},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to start container: %w", err)
	}

	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		container.Terminate(ctx)
		return "", nil, fmt.Errorf("failed to get endpoint: %w", err)
	}

	terminate := func() {
		if err := container.Terminate(ctx); err != nil {
			fmt.Printf("failed to terminate container: %v\n", err)
		}
	}

	return endpoint, terminate, nil
}

func isDockerRunning(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
