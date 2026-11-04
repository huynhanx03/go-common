//go:build integration

package containers

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartRedis starts a Redis container and stops it when the test finishes.
func StartRedis(t testing.TB) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	configureDockerHost(t, ctx)

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        RedisImage,
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start %s container: %v", RedisImage, err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate %s container: %v", RedisImage, err)
		}
	})

	address, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("get %s endpoint: %v", RedisImage, err)
	}
	return address
}
