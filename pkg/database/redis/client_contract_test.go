package redis

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestClientSourceNeverUsesBlockingKEYSCommand(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(source), ".Keys(") {
		t.Fatal("production Redis client uses blocking KEYS command")
	}
	if !strings.Contains(string(source), ".Scan(") {
		t.Fatal("production Redis client does not use bounded SCAN")
	}
}

func TestNewConnectionRejectsNilAndInvalidConfigBeforeDial(t *testing.T) {
	t.Parallel()

	if _, err := NewConnection(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil config error = %v", err)
	}
	config := &settings.Redis{
		Addrs:           []string{"redis.invalid:6379"},
		PoolSize:        -1,
		MinIdleConns:    1,
		PoolTimeout:     1,
		DialTimeout:     1,
		ReadTimeout:     1,
		WriteTimeout:    1,
		MaxRetries:      1,
		MinRetryBackoff: 1,
		MaxRetryBackoff: 2,
	}
	if _, err := NewConnection(config); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid config error = %v", err)
	}
}
