package redis

import (
	"context"

	redisV9 "github.com/redis/go-redis/v9"

	"github.com/huynhanx03/go-common/pkg/dto"
)

// GeoAdd adds locations to a geospatial index.
func (r *RedisEngine) GeoAdd(ctx context.Context, key string, locations ...*dto.GeoLocation) error {
	if len(locations) == 0 {
		return nil
	}
	if len(locations) > maxBatchCommands {
		return ErrBatchTooLarge
	}

	items := make([]*redisV9.GeoLocation, len(locations))
	for i, location := range locations {
		if location == nil {
			return ErrInvalidConfig
		}
		items[i] = &redisV9.GeoLocation{
			Name:      location.Member,
			Longitude: location.Longitude,
			Latitude:  location.Latitude,
		}
	}
	return r.client.GeoAdd(ctx, r.scopedKey(key), items...).Err()
}

// GeoRemove removes members from a geospatial index.
func (r *RedisEngine) GeoRemove(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	if len(members) > maxBatchCommands {
		return ErrBatchTooLarge
	}

	items := make([]any, len(members))
	for i, member := range members {
		items[i] = member
	}
	return r.client.ZRem(ctx, r.scopedKey(key), items...).Err()
}

// GeoRadius returns locations within the given radius, nearest first.
func (r *RedisEngine) GeoRadius(ctx context.Context, key string, longitude, latitude, radius float64, unit string) ([]*dto.GeoLocation, error) {
	items, err := r.client.GeoSearchLocation(ctx, r.scopedKey(key), &redisV9.GeoSearchLocationQuery{
		GeoSearchQuery: redisV9.GeoSearchQuery{
			Longitude:  longitude,
			Latitude:   latitude,
			Radius:     radius,
			RadiusUnit: unit,
			Sort:       "ASC",
		},
		WithCoord: true,
		WithDist:  true,
	}).Result()
	if err != nil {
		return nil, err
	}

	locations := make([]*dto.GeoLocation, len(items))
	for i, item := range items {
		locations[i] = &dto.GeoLocation{
			Member:    item.Name,
			Longitude: item.Longitude,
			Latitude:  item.Latitude,
		}
	}
	return locations, nil
}

// ZAdd adds members to a sorted set.
func (r *RedisEngine) ZAdd(ctx context.Context, key string, members ...*dto.ZMember) error {
	if len(members) == 0 {
		return nil
	}
	if len(members) > maxBatchCommands {
		return ErrBatchTooLarge
	}

	items := make([]redisV9.Z, len(members))
	for i, member := range members {
		if member == nil {
			return ErrInvalidConfig
		}
		items[i] = redisV9.Z{Score: member.Score, Member: member.Member}
	}
	return r.client.ZAdd(ctx, r.scopedKey(key), items...).Err()
}

// ZRemRangeByScore removes members between the given score bounds.
func (r *RedisEngine) ZRemRangeByScore(ctx context.Context, key, min, max string) error {
	return r.client.ZRemRangeByScore(ctx, r.scopedKey(key), min, max).Err()
}

// ZCount counts members between the given score bounds.
func (r *RedisEngine) ZCount(ctx context.Context, key, min, max string) (int64, error) {
	return r.client.ZCount(ctx, r.scopedKey(key), min, max).Result()
}

// ZRange returns members between the given index bounds.
func (r *RedisEngine) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return r.client.ZRange(ctx, r.scopedKey(key), start, stop).Result()
}
