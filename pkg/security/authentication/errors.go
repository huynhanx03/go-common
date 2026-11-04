package authentication

import "errors"

var (
	ErrInvalidPrincipal     = errors.New("invalid principal")
	ErrInvalidPurpose       = errors.New("invalid token purpose")
	ErrInvalidToken         = errors.New("invalid token")
	ErrExpiredToken         = errors.New("expired token")
	ErrUnknownKey           = errors.New("unknown signing key")
	ErrInvalidKeySet        = errors.New("invalid key set")
	ErrInvalidConfiguration = errors.New("invalid configuration")
	ErrIssueRejected        = errors.New("token issue rejected")
	ErrPurposeMismatch      = errors.New("token purpose mismatch")
)
