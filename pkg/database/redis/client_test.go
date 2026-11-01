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

func TestValidateConfigOwnsRedisConfigurationRules(t *testing.T) {
	valid := settings.Redis{
		Addrs:           []string{"redis:6379"},
		PoolSize:        10,
		MinIdleConns:    2,
		PoolTimeout:     5,
		DialTimeout:     5,
		ReadTimeout:     3,
		WriteTimeout:    3,
		MaxRetries:      3,
		MinRetryBackoff: 100,
		MaxRetryBackoff: 500,
	}
	if err := validateConfig(valid); err != nil {
		t.Fatalf("valid Redis config rejected: %v", err)
	}

	tests := []struct {
		name   string
		change func(*settings.Redis)
	}{
		{name: "no addresses", change: func(c *settings.Redis) { c.Addrs = nil }},
		{name: "blank address", change: func(c *settings.Redis) { c.Addrs = []string{"  "} }},
		{name: "negative database", change: func(c *settings.Redis) { c.Database = -1 }},
		{name: "zero pool", change: func(c *settings.Redis) { c.PoolSize = 0 }},
		{name: "pool above limit", change: func(c *settings.Redis) { c.PoolSize = maxPoolSize + 1 }},
		{name: "negative idle connections", change: func(c *settings.Redis) { c.MinIdleConns = -1 }},
		{name: "idle connections above pool", change: func(c *settings.Redis) { c.MinIdleConns = c.PoolSize + 1 }},
		{name: "invalid timeout", change: func(c *settings.Redis) { c.ReadTimeout = 0 }},
		{name: "timeout above limit", change: func(c *settings.Redis) { c.DialTimeout = maxTimeoutSeconds + 1 }},
		{name: "negative retries", change: func(c *settings.Redis) { c.MaxRetries = -1 }},
		{name: "invalid minimum backoff", change: func(c *settings.Redis) { c.MinRetryBackoff = 0 }},
		{name: "maximum backoff below minimum", change: func(c *settings.Redis) { c.MaxRetryBackoff = c.MinRetryBackoff - 1 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.change(&config)
			if err := validateConfig(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateConfig() error = %v; want ErrInvalidConfig", err)
			}
		})
	}
}
