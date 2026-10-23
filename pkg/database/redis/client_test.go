package redis

import (
	"errors"
	"testing"

	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestNewConnectionRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewConnection(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewConnection(nil) error = %v; want ErrInvalidConfig", err)
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
		t.Fatalf("NewConnection(invalid config) error = %v; want ErrInvalidConfig", err)
	}
}

func TestNewConnectionReportsPingFailure(t *testing.T) {
	config := testRedisConfig("127.0.0.1:1")
	config.DialTimeout = 1

	_, err := NewConnection(config)
	if !errors.Is(err, ErrConnectionFailed) || !errors.Is(err, ErrPingFailed) {
		t.Fatalf("NewConnection(unreachable Redis) error = %v; want connection and ping errors", err)
	}
}

func TestSetDefaultConfig(t *testing.T) {
	engine := &RedisEngine{config: &settings.Redis{PoolSize: 2}}
	engine.setDefaultConfig()

	if got, want := engine.config.MinIdleConns, 2; got != want {
		t.Fatalf("MinIdleConns = %d; want %d", got, want)
	}
	if got, want := engine.config.PoolTimeout, defaultPoolTimeout; got != want {
		t.Fatalf("PoolTimeout = %d; want %d", got, want)
	}
	if got, want := engine.config.DialTimeout, defaultDialTimeout; got != want {
		t.Fatalf("DialTimeout = %d; want %d", got, want)
	}
	if got, want := engine.config.ReadTimeout, defaultReadTimeout; got != want {
		t.Fatalf("ReadTimeout = %d; want %d", got, want)
	}
	if got, want := engine.config.WriteTimeout, defaultWriteTimeout; got != want {
		t.Fatalf("WriteTimeout = %d; want %d", got, want)
	}
	if got, want := engine.config.MaxRetries, defaultMaxRetries; got != want {
		t.Fatalf("MaxRetries = %d; want %d", got, want)
	}
	if got, want := engine.config.MinRetryBackoff, defaultMinRetryBackoff; got != want {
		t.Fatalf("MinRetryBackoff = %d; want %d", got, want)
	}
	if got, want := engine.config.MaxRetryBackoff, defaultMaxRetryBackoff; got != want {
		t.Fatalf("MaxRetryBackoff = %d; want %d", got, want)
	}

	engine = &RedisEngine{config: &settings.Redis{}}
	engine.setDefaultConfig()
	if got, want := engine.config.PoolSize, defaultPoolSize; got != want {
		t.Fatalf("default PoolSize = %d; want %d", got, want)
	}
	if got, want := engine.config.MinIdleConns, defaultMinIdleConns; got != want {
		t.Fatalf("default MinIdleConns = %d; want %d", got, want)
	}
}

func testRedisConfig(address string) *settings.Redis {
	return &settings.Redis{
		Addrs:           []string{address},
		PoolSize:        2,
		MinIdleConns:    0,
		PoolTimeout:     1,
		DialTimeout:     1,
		ReadTimeout:     1,
		WriteTimeout:    1,
		MaxRetries:      0,
		MinRetryBackoff: 1,
		MaxRetryBackoff: 2,
	}
}
