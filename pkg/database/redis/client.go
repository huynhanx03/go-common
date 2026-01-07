package redis

import (
	"context"
	"encoding/json"
	"fmt"
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
	defaultMinRetryBackoff = 300 // millis
	defaultMaxRetryBackoff = 500 // millis
)

type RedisEngine struct {
	client  *redisV9.Client
	config  *settings.Redis
	rwMutex sync.Mutex
}

var _ cache.CacheEngine = (*RedisEngine)(nil)

// connect initializes the Redis client
func (r *RedisEngine) connect() error {
	r.setDefaultConfig()

	// Build address
	addr := r.config.Host
	if r.config.Port > 0 {
		addr = fmt.Sprintf("%s:%d", addr, r.config.Port)
	}

	r.client = redisV9.NewClient(&redisV9.Options{
		Addr:            addr,
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

	// Ping test
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrPingFailed, err)
	}

	return nil
}

// setDefaultConfig sets default values for Redis configuration
func (r *RedisEngine) setDefaultConfig() {
	if r.config.PoolSize == 0 {
		r.config.PoolSize = defaultPoolSize
	}
	if r.config.MinIdleConns == 0 {
		r.config.MinIdleConns = defaultMinIdleConns
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

// Get value by key
func (r *RedisEngine) Get(ctx context.Context, key string) ([]byte, bool, error) {
	byteValue, err := r.client.Get(ctx, key).Bytes()
	if err == redisV9.Nil {
		return nil, false, ErrKeyNotFound
	}
	if err != nil {
		return nil, false, err
	}
	return byteValue, true, nil
}

// Delete key
func (r *RedisEngine) Delete(ctx context.Context, key string) error {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()
	return r.client.Del(ctx, key).Err()
}

// InvalidatePrefix invalidates all keys with a given prefix
func (r *RedisEngine) InvalidatePrefix(ctx context.Context, prefix string) error {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()

	val, err := r.client.Keys(ctx, prefix+"*").Result()
	if err != nil {
		return err
	}

	if len(val) > 0 {
		return r.client.Del(ctx, val...).Err()
	}
	return nil
}

// Set value by key
func (r *RedisEngine) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()

	byteValue, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return r.client.Set(ctx, key, byteValue, ttl).Err()
}

// BatchSet stores multiple values in a pipeline
func (r *RedisEngine) BatchSet(ctx context.Context, values map[string]any, ttl time.Duration) error {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()

	pipe := r.client.Pipeline()

	for key, value := range values {
		byteValue, err := json.Marshal(value)
		if err != nil {
			return err
		}
		pipe.Set(ctx, key, byteValue, ttl)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// BatchDelete removes multiple keys from the cache
func (r *RedisEngine) BatchDelete(ctx context.Context, keys []string) error {
	return r.client.Del(ctx, keys...).Err()
}

// GeoAdd adds geospatial locations
func (r *RedisEngine) GeoAdd(ctx context.Context, key string, locations ...*cache.GeoLocation) error {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()

	if len(locations) == 0 {
		return nil
	}

	redisLocations := make([]*redisV9.GeoLocation, len(locations))
	for i, loc := range locations {
		redisLocations[i] = &redisV9.GeoLocation{
			Name:      loc.Member,
			Longitude: loc.Longitude,
			Latitude:  loc.Latitude,
		}
	}

	// Using pipeline for batch add
	pipe := r.client.Pipeline()
	pipe.GeoAdd(ctx, key, redisLocations...)
	_, err := pipe.Exec(ctx)
	return err
}

// GeoRemove removes members from a geospatial index
func (r *RedisEngine) GeoRemove(ctx context.Context, key string, members ...string) error {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()

	if len(members) == 0 {
		return nil
	}

	pipe := r.client.Pipeline()

	interfaceMembers := make([]interface{}, len(members))
	for i, m := range members {
		interfaceMembers[i] = m
	}

	pipe.ZRem(ctx, key, interfaceMembers...)
	_, err := pipe.Exec(ctx)
	return err
}

// Close closes the Redis client
func (r *RedisEngine) Close() {
	if r.client != nil {
		r.client.Close()
	}
}

// Client returns the underlying redis client (Escape hatch)
func (r *RedisEngine) Client() *redisV9.Client {
	return r.client
}

// GeoRadius searches for members within a radius.
func (r *RedisEngine) GeoRadius(ctx context.Context, key string, longitude, latitude, radius float64, unit string) ([]*cache.GeoLocation, error) {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()

	// Use GeoSearchLocation (modern alternative to GeoRadius)
	res, err := r.client.GeoSearchLocation(ctx, key, &redisV9.GeoSearchLocationQuery{
		GeoSearchQuery: redisV9.GeoSearchQuery{
			Longitude:  longitude,
			Latitude:   latitude,
			Radius:     radius,
			RadiusUnit: unit,
			Sort:       "ASC", // Sort by distance
		},
		WithCoord: true,
		WithDist:  true,
	}).Result()

	if err != nil {
		return nil, err
	}

	locations := make([]*cache.GeoLocation, len(res))
	for i, item := range res {
		locations[i] = &cache.GeoLocation{
			Member:    item.Name,
			Longitude: item.Longitude,
			Latitude:  item.Latitude,
		}
	}
	return locations, nil
}
