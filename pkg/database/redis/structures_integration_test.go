//go:build integration

package redis

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/dto"
)

func testRedisStructures(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Helper()

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := engine.GeoRadius(cancelled, "redis-test:cancelled", 0, 0, 1, "km"); err == nil {
		t.Fatal("GeoRadius with a cancelled context should fail")
	}

	t.Run("geospatial index", func(t *testing.T) {
		const key = "redis-test:geo"
		locations := []*dto.GeoLocation{
			{Member: "hanoi", Longitude: 105.8342, Latitude: 21.0278},
			{Member: "saigon", Longitude: 106.6297, Latitude: 10.8231},
		}
		if err := engine.GeoAdd(ctx, key, locations...); err != nil {
			t.Fatalf("GeoAdd: %v", err)
		}
		got, err := engine.GeoRadius(ctx, key, locations[0].Longitude, locations[0].Latitude, 5, "km")
		if err != nil || len(got) != 1 || got[0].Member != "hanoi" {
			t.Fatalf("GeoRadius = %+v, %v; want only hanoi", got, err)
		}
		if err := engine.GeoRemove(ctx, key, "hanoi"); err != nil {
			t.Fatalf("GeoRemove: %v", err)
		}
		got, err = engine.GeoRadius(ctx, key, locations[0].Longitude, locations[0].Latitude, 5, "km")
		if err != nil || len(got) != 0 {
			t.Fatalf("GeoRadius after removal = %+v, %v; want empty", got, err)
		}
		if err := engine.GeoAdd(ctx, key); err != nil {
			t.Fatalf("GeoAdd(empty): %v", err)
		}
		if err := engine.GeoRemove(ctx, key); err != nil {
			t.Fatalf("GeoRemove(empty): %v", err)
		}
	})

	t.Run("sorted set", func(t *testing.T) {
		const key = "redis-test:sorted"
		members := []*dto.ZMember{{Member: "low", Score: 1}, {Member: "high", Score: 3}, {Member: "mid", Score: 2}}
		if err := engine.ZAdd(ctx, key, members...); err != nil {
			t.Fatalf("ZAdd: %v", err)
		}
		if count, err := engine.ZCount(ctx, key, "2", "+inf"); err != nil || count != 2 {
			t.Fatalf("ZCount = %d, %v; want 2", count, err)
		}
		if got, err := engine.ZRange(ctx, key, 0, -1); err != nil || len(got) != 3 || got[0] != "low" || got[2] != "high" {
			t.Fatalf("ZRange = %v, %v; want [low mid high]", got, err)
		}
		if err := engine.ZRemRangeByScore(ctx, key, "-inf", "1"); err != nil {
			t.Fatalf("ZRemRangeByScore: %v", err)
		}
		if got, err := engine.ZRange(ctx, key, 0, -1); err != nil || len(got) != 2 || got[0] != "mid" {
			t.Fatalf("ZRange after score removal = %v, %v; want [mid high]", got, err)
		}
		if err := engine.ZAdd(ctx, key); err != nil {
			t.Fatalf("ZAdd(empty): %v", err)
		}
	})
}
