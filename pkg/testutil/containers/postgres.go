//go:build integration

package containers

import (
	"context"
	"net"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const PostgresImage = "postgres:17-alpine"

// StartPostgres starts an isolated PostgreSQL container for an integration
// test and terminates it during cleanup. The returned DSN is local to the test.
func StartPostgres(t testing.TB) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	configureDockerHost(t, ctx)
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        PostgresImage,
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_DB":       "toggle_test",
				"POSTGRES_USER":     "toggle_test",
				"POSTGRES_PASSWORD": "toggle_test",
			},
			WaitingFor: wait.ForSQL("5432/tcp", "postgres", func(host string, port network.Port) string {
				return "postgres://toggle_test:toggle_test@" + net.JoinHostPort(host, port.Port()) + "/toggle_test?sslmode=disable"
			}).WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start %s container: %v", PostgresImage, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := container.Terminate(cleanupCtx); err != nil {
			t.Errorf("terminate %s container: %v", PostgresImage, err)
		}
	})
	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("get %s endpoint: %v", PostgresImage, err)
	}
	return "postgres://toggle_test:toggle_test@" + endpoint + "/toggle_test?sslmode=disable"
}
