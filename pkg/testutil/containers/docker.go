package containers

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func configureDockerHost(t testing.TB, ctx context.Context) {
	t.Helper()
	if os.Getenv("DOCKER_HOST") != "" {
		return
	}

	output, err := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
	if err != nil {
		return
	}
	if host := strings.TrimSpace(string(output)); host != "" {
		t.Setenv("DOCKER_HOST", host)
	}
}
