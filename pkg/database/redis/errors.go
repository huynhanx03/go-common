package redis

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrConnectionFailed           = errors.New("failed to connect to Redis")
	ErrInvalidConfig              = errors.New("invalid Redis configuration")
	ErrKeyNotFound                = errors.New("key not found")
	ErrPingFailed                 = errors.New("Redis ping failed")
	ErrScanLimit                  = errors.New("Redis scan result limit exceeded")
	ErrBatchTooLarge              = errors.New("Redis batch is too large")
	ErrUnexpectedCacheWriteResult = errors.New("unexpected cache write result")
	ErrInvalidCacheVersion        = errors.New("invalid cache generation")
	ErrInvalidScript              = errors.New("invalid Redis script")
)

func mapCacheVersionError(err error) error {
	if err == nil || !strings.Contains(err.Error(), cacheVersionInvalidCode) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrInvalidCacheVersion, err)
}
