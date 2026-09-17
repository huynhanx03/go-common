package authentication

import "errors"

var (
	ErrInvalidPrincipal     = errors.New("authentication: invalid principal")
	ErrInvalidPurpose       = errors.New("authentication: invalid token purpose")
	ErrInvalidToken         = errors.New("authentication: invalid token")
	ErrExpiredToken         = errors.New("authentication: expired token")
	ErrUnknownKey           = errors.New("authentication: unknown signing key")
	ErrRetiredKey           = errors.New("authentication: retired signing key")
	ErrInvalidKeySet        = errors.New("authentication: invalid key set")
	ErrInvalidConfiguration = errors.New("authentication: invalid configuration")
	ErrIssueRejected        = errors.New("authentication: token issue rejected")
	ErrPurposeMismatch      = errors.New("authentication: token purpose mismatch")
)
