//go:build integration

package redis

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
)

func testCacheRevision(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Helper()

	const key = "redis-test:cache:revision"
	value := []byte{0x00, 0x01, 0xfe, 0xff}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := engine.SetWithVersion(cancelled, key, key, value, time.Minute, "0"); err == nil {
		t.Fatal("SetWithVersion with a cancelled context should fail")
	}
	version, err := engine.SnapshotVersion(ctx, key)
	if err != nil || version != "0" {
		t.Fatalf("initial SnapshotVersion = %q, %v; want 0", version, err)
	}
	if err := engine.SetWithVersion(ctx, key, key, value, time.Minute, version); err != nil {
		t.Fatalf("SetWithVersion: %v", err)
	}
	got, found, err := engine.Get(ctx, key)
	if err != nil || !found || !bytes.Equal(got, value) {
		t.Fatalf("Get raw value = %v, %v, %v; want %v", got, found, err, value)
	}

	if err := engine.DeleteValueWithVersion(ctx, key, key, "stale"); err != nil {
		t.Fatalf("DeleteValueWithVersion(stale): %v", err)
	}
	if _, found, err := engine.Get(ctx, key); err != nil || !found {
		t.Fatalf("stale conditional delete: found %v, error %v; want value retained", found, err)
	}
	if err := engine.DeleteValueWithVersion(ctx, key, key, version); err != nil {
		t.Fatalf("DeleteValueWithVersion(current): %v", err)
	}
	if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get after conditional delete = found %v, error %v; want missing key", found, err)
	}

	if err := engine.SetWithVersion(ctx, key, key, value, time.Minute, version); err != nil {
		t.Fatalf("SetWithVersion before revoke: %v", err)
	}
	if err := engine.Set(ctx, key+cache.NegativeKeySuffix, "negative", time.Minute); err != nil {
		t.Fatalf("Set negative entry: %v", err)
	}
	if err := engine.Revoke(ctx, key); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	version, err = engine.SnapshotVersion(ctx, key)
	if err != nil || version != "1" {
		t.Fatalf("SnapshotVersion after revoke = %q, %v; want 1", version, err)
	}
	if err := engine.SetWithVersion(ctx, key, key, []byte("stale"), time.Minute, "0"); err != nil {
		t.Fatalf("stale SetWithVersion: %v", err)
	}
	if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("stale write after revoke = found %v, error %v; want missing key", found, err)
	}
	if _, found, err := engine.Get(ctx, key+cache.NegativeKeySuffix); found || !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("negative entry after revoke = found %v, error %v; want missing key", found, err)
	}
	if err := engine.Revoke(ctx, key); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	if version, err = engine.SnapshotVersion(ctx, key); err != nil || version != "2" {
		t.Fatalf("SnapshotVersion after second revoke = %q, %v; want 2", version, err)
	}
}

func testCacheBatchRevocation(t *testing.T, ctx context.Context, engine *RedisEngine) {
	t.Helper()

	keys := []string{
		"redis-test:cache:batch:first",
		"redis-test:cache:batch:second",
	}
	versions := make(map[string]string, len(keys))
	for _, key := range keys {
		version, err := engine.SnapshotVersion(ctx, key)
		if err != nil {
			t.Fatalf("SnapshotVersion(%q): %v", key, err)
		}
		versions[key] = version
		if err := engine.SetWithVersion(ctx, key, key, []byte("value"), time.Minute, version); err != nil {
			t.Fatalf("SetWithVersion(%q): %v", key, err)
		}
		if err := engine.Set(ctx, key+cache.NegativeKeySuffix, "negative", time.Minute); err != nil {
			t.Fatalf("Set negative entry for %q: %v", key, err)
		}
	}

	if err := engine.RevokeMany(ctx, keys...); err != nil {
		t.Fatalf("RevokeMany: %v", err)
	}
	for _, key := range keys {
		version, err := engine.SnapshotVersion(ctx, key)
		if err != nil || version == versions[key] {
			t.Fatalf("SnapshotVersion(%q) after batch revoke = %q, %v; want advanced version", key, version, err)
		}
		if err := engine.SetWithVersion(ctx, key, key, []byte("stale"), time.Minute, versions[key]); err != nil {
			t.Fatalf("stale SetWithVersion(%q): %v", key, err)
		}
		if _, found, err := engine.Get(ctx, key); found || !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Get(%q) after batch revoke = found %v, error %v; want missing", key, found, err)
		}
		if _, found, err := engine.Get(ctx, key+cache.NegativeKeySuffix); found || !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Get negative entry for %q = found %v, error %v; want missing", key, found, err)
		}
	}
}
