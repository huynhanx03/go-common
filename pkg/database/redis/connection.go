package redis

import (
	"errors"
	"fmt"

	"github.com/huynhanx03/go-common/pkg/settings"
)

// NewConnection creates and returns a new Redis client
func NewConnection(cfg *settings.Redis) (*RedisEngine, error) {
	if cfg == nil {
		return nil, ErrInvalidConfig
	}
	owned := *cfg
	engine := &RedisEngine{
		config: &owned,
	}

	if err := engine.connect(); err != nil {
		if errors.Is(err, ErrInvalidConfig) {
			return nil, err
		}
		return nil, errors.Join(
			ErrConnectionFailed,
			fmt.Errorf("connect redis: %w", err),
		)
	}

	return engine, nil
}
