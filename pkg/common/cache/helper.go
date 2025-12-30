package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
)

// HandleHitCache handles cache hit
func HandleHitCache(ctx context.Context, model any, c CacheEngine, key string) error {
	byteData, exists, err := c.Get(ctx, key)
	if exists && err == nil {
		err = json.Unmarshal(byteData, model)
		if err != nil {
			return errors.Wrap(err, "failed to unmarshal cache")
		}
		return nil
	}
	return errors.Wrap(err, "miss cache")
}

// HandleSetCache handles cache set
func HandleSetCache(ctx context.Context, model any, c CacheEngine, key string, ttl time.Duration) error {
	return c.Set(ctx, key, model, ttl)
}
