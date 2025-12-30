package mongodb

import "errors"

var (
	// ErrConnectFailed is returned when connection to MongoDB fails.
	ErrConnectFailed = errors.New("failed to connect to MongoDB")

	// ErrPingFailed is returned when ping to MongoDB fails.
	ErrPingFailed = errors.New("failed to ping MongoDB")

	// ErrDisconnectFailed is returned when disconnection from MongoDB fails.
	ErrDisconnectFailed = errors.New("failed to disconnect from MongoDB")
)
