package widecolumn

import "errors"

var (
	ErrConnectFailed           = errors.New("failed to connect to database")
	ErrPingFailed              = errors.New("failed to ping database")
	ErrDisconnectFailed        = errors.New("failed to disconnect from database")
	ErrNotFound                = errors.New("record not found")
	ErrInvalidID               = errors.New("invalid id")
	ErrNilSession              = errors.New("widecolumn: nil session")
	ErrInvalidContext          = errors.New("widecolumn: nil context")
	ErrInvalidModel            = errors.New("widecolumn: invalid model")
	ErrInvalidIdentifier       = errors.New("widecolumn: invalid identifier")
	ErrInvalidConfiguration    = errors.New("widecolumn: invalid configuration")
	ErrUnsupportedQuery        = errors.New("widecolumn: unsupported generic query")
	ErrInvalidCursor           = errors.New("widecolumn: invalid paging cursor")
	ErrInvalidTTL              = errors.New("widecolumn: invalid TTL")
	ErrBatchTooLarge           = errors.New("widecolumn: batch too large")
	ErrUninitializedRepository = errors.New("widecolumn: uninitialized repository")
	ErrNilConfiguration        = errors.New("widecolumn: nil configuration")
	ErrUninitializedClient     = errors.New("widecolumn: uninitialized client")
	ErrNotConnected            = errors.New("widecolumn: client is not connected")
)
