package cache

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
)

type tierCoordinator struct {
	l1  L1Engine
	l2  L2Engine
	log *logger.Logger
}

type tierVersion struct {
	token string
	valid bool
}

type tierVersions struct {
	l1 tierVersion
	l2 tierVersion
}

func (t tierCoordinator) getL1(ctx context.Context, key string) (any, bool) {
	if t.l1 == nil {
		return nil, false
	}
	return t.l1.Get(ctx, key)
}

func (t tierCoordinator) getL2(ctx context.Context, key, operation string) ([]byte, bool) {
	if t.l2 == nil {
		return nil, false
	}

	value, found, err := t.l2.Get(ctx, key)
	if err != nil || !found {
		if !errors.Is(err, ErrKeyNotFound) {
			t.reportError(ctx, operation, err)
		}
		return nil, false
	}
	return value, true
}

func (t tierCoordinator) snapshot(ctx context.Context, key string) tierVersions {
	return tierVersions{
		l1: t.snapshotL1(ctx, key),
		l2: t.snapshotL2(ctx, key),
	}
}

func (t tierCoordinator) snapshotL1(ctx context.Context, key string) tierVersion {
	if t.l1 == nil {
		return tierVersion{}
	}
	token, err := t.l1.SnapshotVersion(ctx, key)
	t.reportError(ctx, operationL1SnapshotVersion, err)
	return tierVersion{token: token, valid: err == nil}
}

func (t tierCoordinator) getL2ForPromotion(ctx context.Context, versionKey, storageKey, operation string) ([]byte, tierVersion, bool) {
	if t.l2 == nil {
		return nil, tierVersion{}, false
	}

	version := t.snapshotL1(ctx, versionKey)
	value, found := t.getL2(ctx, storageKey, operation)
	return value, version, found
}

func (t tierCoordinator) snapshotL2(ctx context.Context, key string) tierVersion {
	if t.l2 == nil {
		return tierVersion{}
	}
	token, err := t.l2.SnapshotVersion(ctx, key)
	t.reportError(ctx, operationL2SnapshotVersion, err)
	return tierVersion{token: token, valid: err == nil}
}

func (t tierCoordinator) setL1(ctx context.Context, versionKey, storageKey string, value any, ttl time.Duration, version tierVersion, operation string) {
	if !version.valid {
		return
	}
	t.reportError(ctx, operation, t.l1.SetWithVersion(ctx, versionKey, storageKey, value, ttl, version.token))
}

func (t tierCoordinator) writeL2(ctx context.Context, versionKey, storageKey string, value []byte, ttl time.Duration, version tierVersion, operation string) {
	t.reportError(ctx, operation, t.l2.SetWithVersion(ctx, versionKey, storageKey, value, ttl, version.token))
}

func (t tierCoordinator) deleteL1(ctx context.Context, versionKey, storageKey string, version tierVersion, operation string) {
	if !version.valid {
		return
	}
	t.reportError(ctx, operation, t.l1.DeleteValueWithVersion(ctx, versionKey, storageKey, version.token))
}

func (t tierCoordinator) deleteL2(ctx context.Context, versionKey, storageKey string, version tierVersion, operation string) {
	if !version.valid {
		return
	}
	t.reportError(ctx, operation, t.l2.DeleteValueWithVersion(ctx, versionKey, storageKey, version.token))
}

func storeEntryL2[T any](ctx context.Context, tiers tierCoordinator, key string, value entry[T], ttl time.Duration, version tierVersion) {
	if !version.valid {
		return
	}
	data, err := encodeEntry(value)
	if err != nil {
		tiers.reportError(ctx, operationL2Encode, err)
		return
	}
	tiers.writeL2(ctx, key, key, data, ttl, version, operationL2Set)
}

func (t tierCoordinator) storeNegative(ctx context.Context, key string, ttl time.Duration, versions tierVersions) {
	t.deleteL1(ctx, key, key, versions.l1, operationL1DeleteMissing)
	t.setL1(ctx, key, key+NegativeKeySuffix, entry[struct{}]{ExpireAt: time.Now().Add(ttl).UnixMilli(), Negative: true}, ttl, versions.l1, operationL1SetNegative)
	t.deleteL2(ctx, key, key, versions.l2, operationL2DeleteMissing)
	if versions.l2.valid {
		data, err := encodeNegativeEntry(ttl)
		if err != nil {
			t.reportError(ctx, operationL2EncodeNegative, err)
			return
		}
		t.writeL2(ctx, key, key+NegativeKeySuffix, data, ttl, versions.l2, operationL2SetNegative)
	}
}

func (t tierCoordinator) revoke(ctx context.Context, key string) error {
	var errs []error
	if t.l2 != nil {
		errs = append(errs, t.l2.Revoke(ctx, key))
	}
	if t.l1 != nil {
		errs = append(errs, t.l1.Revoke(ctx, key))
	}
	return errors.Join(errs...)
}

func (t tierCoordinator) revokeMany(ctx context.Context, keys ...string) error {
	var errs []error
	if t.l2 != nil {
		errs = append(errs, t.l2.RevokeMany(ctx, keys...))
	}
	if t.l1 != nil {
		errs = append(errs, t.l1.RevokeMany(ctx, keys...))
	}
	return errors.Join(errs...)
}

// Delete invalidates a key in every configured tier.
func (c *Cache) Delete(ctx context.Context, key string) error {
	if key == "" || len(key) > MaxKeyBytes {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.tiers.revoke(ctx, key)
}

// DeleteAll invalidates each key in every configured tier.
func (c *Cache) DeleteAll(ctx context.Context, keys ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	var errs []error
	for _, key := range keys {
		if key == "" || len(key) > MaxKeyBytes {
			errs = append(errs, ErrInvalidConfig)
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	return errors.Join(append(errs, c.tiers.revokeMany(ctx, unique...))...)
}

func (t tierCoordinator) close() {
	if t.l1 != nil {
		t.l1.Close()
	}
	if t.l2 != nil {
		t.l2.Close()
	}
}

func (t tierCoordinator) reportError(ctx context.Context, operation string, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}
	t.log.Error(ctx,
		"cache operation failed",
		zap.String("operation", operation),
		zap.Error(err),
	)
}
