package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	redisV9 "github.com/redis/go-redis/v9"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/utils"
)

const (
	defaultPoolSize        = 10
	defaultMinIdleConns    = 5
	defaultPoolTimeout     = 5
	defaultDialTimeout     = 5
	defaultReadTimeout     = 3
	defaultWriteTimeout    = 3
	defaultMaxRetries      = 3
	defaultMinRetryBackoff = 300
	defaultMaxRetryBackoff = 500
)

type RedisEngine struct {
	client    redisV9.UniversalClient
	config    *settings.Redis
	namespace string
	closeOnce sync.Once
}

var _ cache.L2Engine = (*RedisEngine)(nil)

// NewConnection creates and returns a new Redis client
func NewConnection(cfg *settings.Redis) (*RedisEngine, error) {
	if cfg == nil {
		return nil, ErrInvalidConfig
	}
	owned := *cfg
	engine := &RedisEngine{
		config:    &owned,
		namespace: strings.Trim(strings.TrimSpace(cfg.Namespace), ":"),
	}

	if err := engine.connect(); err != nil {
		if errors.Is(err, ErrInvalidConfig) {
			return nil, err
		}
		return nil, errors.Join(
			ErrConnectionFailed,
			fmt.Errorf("connect to Redis: %w", err),
		)
	}

	return engine, nil
}

func (r *RedisEngine) connect() error {
	r.setDefaultConfig()
	if err := r.config.Validate(); err != nil {
		return errors.Join(ErrInvalidConfig, err)
	}

	r.client = redisV9.NewUniversalClient(&redisV9.UniversalOptions{
		Addrs:           r.config.Addrs,
		MasterName:      r.config.MasterName,
		Password:        r.config.Password,
		DB:              r.config.Database,
		PoolSize:        r.config.PoolSize,
		MinIdleConns:    r.config.MinIdleConns,
		MaxRetries:      r.config.MaxRetries,
		DialTimeout:     utils.ToDuration(r.config.DialTimeout),
		ReadTimeout:     utils.ToDuration(r.config.ReadTimeout),
		WriteTimeout:    utils.ToDuration(r.config.WriteTimeout),
		PoolTimeout:     utils.ToDuration(r.config.PoolTimeout),
		MinRetryBackoff: utils.ToDurationMs(r.config.MinRetryBackoff),
		MaxRetryBackoff: utils.ToDurationMs(r.config.MaxRetryBackoff),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.client.Ping(ctx).Err(); err != nil {
		_ = r.client.Close()
		return errors.Join(ErrPingFailed, fmt.Errorf("ping Redis: %w", err))
	}
	return nil
}

func (r *RedisEngine) setDefaultConfig() {
	if r.config.PoolSize == 0 {
		r.config.PoolSize = defaultPoolSize
	}
	if r.config.MinIdleConns == 0 {
		r.config.MinIdleConns = min(defaultMinIdleConns, r.config.PoolSize)
	}
	if r.config.PoolTimeout == 0 {
		r.config.PoolTimeout = defaultPoolTimeout
	}
	if r.config.DialTimeout == 0 {
		r.config.DialTimeout = defaultDialTimeout
	}
	if r.config.ReadTimeout == 0 {
		r.config.ReadTimeout = defaultReadTimeout
	}
	if r.config.WriteTimeout == 0 {
		r.config.WriteTimeout = defaultWriteTimeout
	}
	if r.config.MaxRetries == 0 {
		r.config.MaxRetries = defaultMaxRetries
	}
	if r.config.MinRetryBackoff == 0 {
		r.config.MinRetryBackoff = defaultMinRetryBackoff
	}
	if r.config.MaxRetryBackoff == 0 {
		r.config.MaxRetryBackoff = defaultMaxRetryBackoff
	}
}

// Close closes the Redis client once.
func (r *RedisEngine) Close() {
	r.closeOnce.Do(func() {
		_ = r.client.Close()
	})
}

// Client returns the underlying Redis client. Direct commands do not apply Namespace.
func (r *RedisEngine) Client() redisV9.UniversalClient {
	return r.client
}
