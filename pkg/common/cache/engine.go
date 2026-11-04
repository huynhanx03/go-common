package cache

import (
	"context"
	"time"
)

// L1Engine is process-local; invalidations from other instances are bounded by
// the L1 entry's TTL unless the backend propagates them. Operations receive the
// request context for cancellation and request-scoped logging.
type L1Engine interface {
	Get(ctx context.Context, key string) (any, bool)
	SetWithTTL(ctx context.Context, key string, value any, ttl time.Duration) bool
	SnapshotVersion(ctx context.Context, key string) (string, error)
	SetWithVersion(ctx context.Context, versionKey, storageKey string, value any, ttl time.Duration, token string) error
	DeleteValueWithVersion(ctx context.Context, versionKey, storageKey, token string) error
	Revoke(ctx context.Context, key string) error
	RevokeMany(ctx context.Context, keys ...string) error
	Close()
}

// L2Engine is the shared byte tier; Fetch encodes objects only when writing here.
type L2Engine interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	SnapshotVersion(ctx context.Context, key string) (string, error)
	SetWithVersion(ctx context.Context, versionKey, storageKey string, value []byte, ttl time.Duration, token string) error
	DeleteValueWithVersion(ctx context.Context, versionKey, storageKey, token string) error
	Revoke(ctx context.Context, key string) error
	RevokeMany(ctx context.Context, keys ...string) error
	Close()
}
