//go:build integration

package containers

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TemporalServer owns a local file-backed Temporal development server.
type TemporalServer struct {
	container testcontainers.Container
	address   string
}

// StartTemporal starts one isolated Temporal CLI server for an integration test.
func StartTemporal(t testing.TB) *TemporalServer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	configureDockerHost(t, ctx)

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        TemporalImage,
			ExposedPorts: []string{"7233/tcp", "8233/tcp"},
			Cmd: []string{
				"server", "start-dev",
				"--ip", "0.0.0.0",
				"--ui-ip", "0.0.0.0",
				"--port", "7233",
				"--ui-port", "8233",
				"--db-filename", "/tmp/temporal.db",
			},
			WaitingFor: wait.ForListeningPort("7233/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start %s container: %v", TemporalImage, err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate %s container: %v", TemporalImage, err)
		}
	})

	address, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("get %s endpoint: %v", TemporalImage, err)
	}
	server := &TemporalServer{container: container, address: address}
	if err := server.waitReady(ctx); err != nil {
		t.Fatalf("wait for Temporal server readiness: %v", err)
	}
	return server
}

// Address returns the host-mapped Temporal gRPC address.
func (server *TemporalServer) Address() string { return server.address }

// Restart restarts the same container and waits for the persisted server to become ready.
func (server *TemporalServer) Restart(ctx context.Context) error {
	if err := server.container.Stop(ctx, nil); err != nil {
		return fmt.Errorf("stop Temporal server: %w", err)
	}
	if err := server.container.Start(ctx); err != nil {
		return fmt.Errorf("start Temporal server: %w", err)
	}
	address, err := server.container.Endpoint(ctx, "")
	if err != nil {
		return fmt.Errorf("get restarted Temporal endpoint: %w", err)
	}
	server.address = address
	return server.waitReady(ctx)
}

func (server *TemporalServer) waitReady(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastOutput string
	for {
		exitCode, output, err := server.container.Exec(ctx, []string{"temporal", "operator", "cluster", "health"})
		if output != nil {
			response, readErr := io.ReadAll(output)
			if readErr == nil {
				lastOutput = string(response)
			}
		}
		if err == nil && exitCode == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Temporal server is not ready: %s: %w", lastOutput, ctx.Err())
		case <-ticker.C:
		}
	}
}
