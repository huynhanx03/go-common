package redis

import "errors"

var (
	// ErrKeyNotFound is returned when a key is not found in Redis.
	ErrKeyNotFound = errors.New("key not found")

	// ErrPingFailed is returned when the Redis ping fails.
	ErrPingFailed = errors.New("redis ping failed")

	// ErrConnectionFailed is returned when the connection to Redis fails.
	ErrConnectionFailed = errors.New("failed to connect to Redis")

	ErrInvalidConfig = errors.New("redis: invalid configuration")
	ErrScanLimit     = errors.New("redis: scan result limit exceeded")
	ErrBatchTooLarge = errors.New("redis: batch too large")
)
