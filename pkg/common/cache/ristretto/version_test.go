package ristretto

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
)

func TestVersionFenceProtectsWritesAndRevokesEntries(t *testing.T) {
	c := newTestCache(t)
	version, err := c.SnapshotVersion(context.Background(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.SnapshotVersion(context.Background(), "profile"); err != nil || again != version {
		t.Fatalf("SnapshotVersion() = %q, %v; want stable token %q", again, err, version)
	}

	if err := c.SetWithVersion(context.Background(), "profile", "profile", "fresh", time.Minute, version); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(context.Background(), "profile"); !ok || got != "fresh" {
		t.Fatalf("Get() = %v, %v; want fresh", got, ok)
	}
	if err := c.SetWithVersion(context.Background(), "profile", "profile", "stale", time.Minute, "old-token"); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(context.Background(), "profile"); !ok || got != "fresh" {
		t.Fatalf("stale SetWithVersion changed entry: Get() = %v, %v", got, ok)
	}

	if err := c.DeleteValueWithVersion(context.Background(), "profile", "profile", "old-token"); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(context.Background(), "profile"); !ok || got != "fresh" {
		t.Fatalf("stale DeleteValueWithVersion removed entry: Get() = %v, %v", got, ok)
	}
	if err := c.DeleteValueWithVersion(context.Background(), "profile", "profile", version); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(context.Background(), "profile"); ok {
		t.Fatal("matching DeleteValueWithVersion left entry")
	}

	if err := c.SetWithVersion(context.Background(), "profile", "profile", "stale", time.Minute, version); err != nil {
		t.Fatal(err)
	}
	if err := c.SetWithVersion(context.Background(), "profile", "profile"+cache.NegativeKeySuffix, "negative", time.Minute, version); err != nil {
		t.Fatal(err)
	}
	if err := c.Revoke(context.Background(), "profile"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(context.Background(), "profile"); ok {
		t.Fatal("Revoke left cached value")
	}
	if _, ok := c.Get(context.Background(), "profile"+cache.NegativeKeySuffix); ok {
		t.Fatal("Revoke left negative entry")
	}
	if err := c.SetWithVersion(context.Background(), "profile", "profile", "stale", time.Minute, version); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(context.Background(), "profile"); ok {
		t.Fatal("old token wrote after Revoke")
	}
}

func TestRevokeManyFencesAndDeletesEntries(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	keys := []string{"first", "second", "first"}
	versions := make(map[string]string, len(keys))
	for _, key := range keys {
		version, err := c.SnapshotVersion(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		versions[key] = version
		for _, storageKey := range []string{key, key + cache.NegativeKeySuffix} {
			if err := c.SetWithVersion(ctx, key, storageKey, storageKey, time.Minute, version); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := c.RevokeMany(ctx, keys...); err != nil {
		t.Fatalf("RevokeMany() error = %v", err)
	}
	for _, key := range keys {
		for _, storageKey := range []string{key, key + cache.NegativeKeySuffix} {
			if _, found := c.Get(ctx, storageKey); found {
				t.Errorf("RevokeMany() left %q in the cache", storageKey)
			}
		}
		if err := c.SetWithVersion(ctx, key, key, "stale", time.Minute, versions[key]); err != nil {
			t.Fatal(err)
		}
		if _, found := c.Get(ctx, key); found {
			t.Errorf("RevokeMany() allowed stale write for %q", key)
		}
	}
}

func TestInvalidVersionRequests(t *testing.T) {
	c := newTestCache(t)
	if _, err := c.SnapshotVersion(context.Background(), ""); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("SnapshotVersion(empty) error = %v", err)
	}
	if _, err := c.SnapshotVersion(context.Background(), string(make([]byte, cache.MaxKeyBytes+1))); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("SnapshotVersion(long) error = %v", err)
	}
	if err := c.SetWithVersion(context.Background(), "", "value", 1, time.Minute, "token"); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("SetWithVersion(empty version key) error = %v", err)
	}
	if err := c.SetWithVersion(context.Background(), "key", "", 1, time.Minute, "token"); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("SetWithVersion(empty storage key) error = %v", err)
	}
	if err := c.SetWithVersion(context.Background(), "key", "value", 1, 0, "token"); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("SetWithVersion(non-positive ttl) error = %v", err)
	}
	if err := c.SetWithVersion(context.Background(), "key", "value", 1, time.Minute, ""); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("SetWithVersion(empty token) error = %v", err)
	}
	if err := c.DeleteValueWithVersion(context.Background(), "", "value", "token"); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("DeleteValueWithVersion(empty version key) error = %v", err)
	}
	if err := c.DeleteValueWithVersion(context.Background(), "key", "", "token"); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("DeleteValueWithVersion(empty storage key) error = %v", err)
	}
	if err := c.DeleteValueWithVersion(context.Background(), "key", "value", ""); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("DeleteValueWithVersion(empty token) error = %v", err)
	}
	if err := c.Revoke(context.Background(), ""); !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("Revoke(empty key) error = %v", err)
	}
}

func TestVersionOperationsHonorCanceledContext(t *testing.T) {
	c := newTestCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.SnapshotVersion(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SnapshotVersion() error = %v, want context.Canceled", err)
	}
	if err := c.SetWithVersion(ctx, "key", "key", 1, time.Minute, "token"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetWithVersion() error = %v, want context.Canceled", err)
	}
	if err := c.DeleteValueWithVersion(ctx, "key", "key", "token"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteValueWithVersion() error = %v, want context.Canceled", err)
	}
	if err := c.Revoke(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Revoke() error = %v, want context.Canceled", err)
	}
}

func TestVersionOperationsFailAfterClose(t *testing.T) {
	c := newTestCache(t)
	c.Close()
	if _, err := c.SnapshotVersion(context.Background(), "key"); err == nil {
		t.Fatal("SnapshotVersion succeeded after Close")
	}
	if err := c.SetWithVersion(context.Background(), "key", "key", 1, time.Minute, "token"); err == nil {
		t.Fatal("SetWithVersion succeeded after Close")
	}
	if err := c.DeleteValueWithVersion(context.Background(), "key", "key", "token"); err == nil {
		t.Fatal("DeleteValueWithVersion succeeded after Close")
	}
	if err := c.Revoke(context.Background(), "key"); err == nil {
		t.Fatal("Revoke succeeded after Close")
	}
}

func TestVersionOperationsReportRistrettoWriteRejection(t *testing.T) {
	c := newTestCache(t)
	c.inner.Close()
	if err := c.writeVersionedValue(hashKey("key"), "value", time.Minute); !errors.Is(err, errConditionalWriteFailed) {
		t.Fatalf("writeVersionedValue() error = %v; want %v", err, errConditionalWriteFailed)
	}
	if err := c.Revoke(context.Background(), "key"); err == nil {
		t.Fatal("Revoke hid generation write rejection")
	}
	if _, err := c.SnapshotVersion(context.Background(), "new-key"); err == nil {
		t.Fatal("SnapshotVersion hid generation write rejection")
	}
}

func TestGenerationReportsRandomSourceFailure(t *testing.T) {
	previous := rand.Reader
	rand.Reader = errorReader{}
	t.Cleanup(func() { rand.Reader = previous })
	c := newTestCache(t)
	if _, err := c.SnapshotVersion(context.Background(), "key"); err == nil {
		t.Fatal("SnapshotVersion hid random source failure")
	}
	if err := c.Revoke(context.Background(), "key"); err == nil {
		t.Fatal("Revoke hid random source failure")
	}
	if err := c.RevokeMany(context.Background(), "key"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("RevokeMany() error = %v; want random source error", err)
	}
}

func TestRevokeManyHandlesEmptyInvalidCanceledAndClosedRequests(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	if err := c.RevokeMany(ctx); err != nil {
		t.Fatalf("RevokeMany() with no keys = %v", err)
	}
	for _, keys := range [][]string{{""}, {string(make([]byte, cache.MaxKeyBytes+1))}} {
		if err := c.RevokeMany(ctx, keys...); !errors.Is(err, cache.ErrInvalidConfig) {
			t.Errorf("RevokeMany(%d-byte key) = %v", len(keys[0]), err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.RevokeMany(canceled, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("RevokeMany(canceled) = %v", err)
	}
	c.Close()
	if err := c.RevokeMany(ctx, "key"); err == nil {
		t.Fatal("RevokeMany succeeded after Close")
	}
}

func TestRevokeManyReturnsRejectedGenerationWrites(t *testing.T) {
	c := newTestCache(t)
	c.inner.Close()
	if err := c.RevokeMany(context.Background(), "one", "two"); !errors.Is(err, errGenerationWriteRejected) {
		t.Fatalf("RevokeMany() error = %v; want generation write rejection", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
