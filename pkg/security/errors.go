package security

import "errors"

var (
	ErrUnauthenticated      = errors.New("authentication required")
	ErrForbidden            = errors.New("permission denied")
	ErrInvalidConfiguration = errors.New("invalid security configuration")
	ErrInvalidRequest       = errors.New("invalid security request")
)
