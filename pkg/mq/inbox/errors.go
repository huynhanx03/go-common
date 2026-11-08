package inbox

import "errors"

var (
	// ErrInvalidConfig marks missing inbox dependencies or consumer name.
	ErrInvalidConfig = errors.New("invalid inbox configuration")
	// ErrMissingMessageID marks a message that cannot be deduplicated safely.
	ErrMissingMessageID = errors.New("message ID is required")
	// ErrInvalidMessageID marks a message with an unusable identity.
	ErrInvalidMessageID = errors.New("message ID is invalid")
	// ErrInvalidTransaction marks a store result without a usable transaction.
	ErrInvalidTransaction = errors.New("inbox transaction is invalid")
)
