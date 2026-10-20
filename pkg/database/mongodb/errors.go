package mongodb

import "errors"

var (
	// ErrConnectFailed is returned when connection to MongoDB fails.
	ErrConnectFailed = errors.New("failed to connect to MongoDB")

	// ErrPingFailed is returned when ping to MongoDB fails.
	ErrPingFailed = errors.New("failed to ping MongoDB")

	// ErrDisconnectFailed is returned when disconnection from MongoDB fails.
	ErrDisconnectFailed = errors.New("failed to disconnect from MongoDB")

	ErrInvalidConfig     = errors.New("invalid MongoDB configuration")
	ErrInvalidRepository = errors.New("invalid MongoDB repository")
	ErrInvalidQuery      = errors.New("invalid MongoDB query")
	ErrInvalidIdentifier = errors.New("invalid MongoDB field identifier")
	ErrInvalidID         = errors.New("invalid MongoDB document id")
	ErrInvalidContext    = errors.New("invalid MongoDB context")
	ErrInvalidCursor     = errors.New("invalid MongoDB cursor")
	ErrBatchTooLarge     = errors.New("MongoDB batch too large")
	ErrEncodeFailed      = errors.New("failed to encode MongoDB document")
)
