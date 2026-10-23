package logger

import "errors"

var (
	// ErrInvalidLogger reports use of an uninitialized logger.
	ErrInvalidLogger = errors.New("invalid logger")
	// ErrInvalidConfig reports invalid logger configuration.
	ErrInvalidConfig = errors.New("invalid logger configuration")
	// ErrInvalidRequestID reports an unsafe request ID.
	ErrInvalidRequestID = errors.New("invalid request ID")
)
