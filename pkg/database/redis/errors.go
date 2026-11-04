package redis

import "errors"

var (
	ErrConnectionFailed           = errors.New("failed to connect to Redis")
	ErrInvalidConfig              = errors.New("invalid Redis configuration")
	ErrKeyNotFound                = errors.New("key not found")
	ErrPingFailed                 = errors.New("Redis ping failed")
	ErrScanLimit                  = errors.New("Redis scan result limit exceeded")
	ErrBatchTooLarge              = errors.New("Redis batch is too large")
	ErrUnexpectedCacheWriteResult = errors.New("unexpected cache write result")
)
