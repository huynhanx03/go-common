package password

import "errors"

var (
	ErrInvalidPolicy   = errors.New("invalid policy")
	ErrEmptyPassword   = errors.New("empty password")
	ErrPasswordTooLong = errors.New("password too long")
	ErrMalformedHash   = errors.New("malformed hash")
	ErrUnsupportedHash = errors.New("unsupported hash")
	ErrHashPolicy      = errors.New("hash exceeds verification policy")
	ErrInvalidContext  = errors.New("nil context")
)
