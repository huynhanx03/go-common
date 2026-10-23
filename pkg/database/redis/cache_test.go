package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
)

func TestCacheMethodsRejectInvalidArguments(t *testing.T) {
	engine := &RedisEngine{}
	ctx := context.Background()
	if _, err := engine.SnapshotVersion(ctx, ""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("SnapshotVersion(empty key) error = %v; want ErrInvalidConfig", err)
	}
	if _, err := engine.SnapshotVersion(ctx, string(make([]byte, cache.MaxKeyBytes+1))); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("SnapshotVersion(long key) error = %v; want ErrInvalidConfig", err)
	}
	for _, test := range []struct {
		name string
		call func() error
	}{
		{name: "empty version key", call: func() error { return engine.SetWithVersion(ctx, "", "value", nil, time.Second, "token") }},
		{name: "empty storage key", call: func() error { return engine.SetWithVersion(ctx, "version", "", nil, time.Second, "token") }},
		{name: "empty token", call: func() error { return engine.SetWithVersion(ctx, "version", "value", nil, time.Second, "") }},
		{name: "nonpositive ttl", call: func() error { return engine.SetWithVersion(ctx, "version", "value", nil, 0, "token") }},
		{name: "empty delete version key", call: func() error { return engine.DeleteValueWithVersion(ctx, "", "value", "token") }},
		{name: "empty delete storage key", call: func() error { return engine.DeleteValueWithVersion(ctx, "version", "", "token") }},
		{name: "empty delete token", call: func() error { return engine.DeleteValueWithVersion(ctx, "version", "value", "") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v; want ErrInvalidConfig", err)
			}
		})
	}
	if err := engine.Revoke(ctx, ""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Revoke(empty key) error = %v; want ErrInvalidConfig", err)
	}
}

func TestValidateCacheWriteResult(t *testing.T) {
	for _, result := range []int64{0, 1} {
		if err := validateCacheWriteResult(result); err != nil {
			t.Errorf("validateCacheWriteResult(%d) = %v; want nil", result, err)
		}
	}

	err := validateCacheWriteResult(2)
	if !errors.Is(err, ErrUnexpectedCacheWriteResult) {
		t.Fatalf("validateCacheWriteResult(2) = %v; want ErrUnexpectedCacheWriteResult", err)
	}
	if want := "unexpected cache write result: 2"; err.Error() != want {
		t.Fatalf("unexpected-result error string = %q; want %q", err, want)
	}
}

func TestCacheGenerationKeyUsesRedisHashTag(t *testing.T) {
	for _, test := range []struct {
		key  string
		want string
	}{
		{key: "profile", want: "__cache_generation__:{profile}:profile"},
		{key: "cache:{account}:profile", want: "__cache_generation__:{account}:cache:{account}:profile"},
		{key: "cache:{broken", want: "__cache_generation__:{cache:{broken}:cache:{broken"},
		{key: "cache:{}:profile", want: "__cache_generation__:{cache:{}:profile}:cache:{}:profile"},
	} {
		if got := cacheGenerationKey(test.key); got != test.want {
			t.Errorf("cacheGenerationKey(%q) = %q; want %q", test.key, got, test.want)
		}
	}
}
