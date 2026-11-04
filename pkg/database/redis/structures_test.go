package redis

import (
	"context"
	"errors"
	"testing"

	"github.com/huynhanx03/go-common/pkg/dto"
)

func TestRedisStructuresRejectInvalidBatchMembers(t *testing.T) {
	engine := &RedisEngine{}
	ctx := context.Background()
	if err := engine.GeoAdd(ctx, "key", (*dto.GeoLocation)(nil)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("GeoAdd(nil member) error = %v; want ErrInvalidConfig", err)
	}
	if err := engine.ZAdd(ctx, "key", (*dto.ZMember)(nil)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("ZAdd(nil member) error = %v; want ErrInvalidConfig", err)
	}
	if err := engine.GeoAdd(ctx, "key", makeGeoLocations(maxBatchCommands+1)...); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("GeoAdd over limit error = %v; want ErrBatchTooLarge", err)
	}
	if err := engine.GeoRemove(ctx, "key", make([]string, maxBatchCommands+1)...); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("GeoRemove over limit error = %v; want ErrBatchTooLarge", err)
	}
	if err := engine.ZAdd(ctx, "key", makeZMembers(maxBatchCommands+1)...); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("ZAdd over limit error = %v; want ErrBatchTooLarge", err)
	}
}

func makeGeoLocations(size int) []*dto.GeoLocation {
	locations := make([]*dto.GeoLocation, size)
	for i := range locations {
		locations[i] = &dto.GeoLocation{Member: "member"}
	}
	return locations
}

func makeZMembers(size int) []*dto.ZMember {
	members := make([]*dto.ZMember, size)
	for i := range members {
		members[i] = &dto.ZMember{Member: "member"}
	}
	return members
}
