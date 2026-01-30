package widecolumn

import "errors"

var (
	ErrConnectFailed    = errors.New("failed to connect to database")
	ErrPingFailed       = errors.New("failed to ping database")
	ErrDisconnectFailed = errors.New("failed to disconnect from database")
	ErrNotFound         = errors.New("record not found")
	ErrInvalidID        = errors.New("invalid id")
)
