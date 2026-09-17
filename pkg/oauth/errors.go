package oauth

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidConfiguration  = errors.New("oauth: invalid configuration")
	ErrInvalidRequest        = errors.New("oauth: invalid request")
	ErrCompatibilityDisabled = errors.New("oauth: compatibility disabled")
	ErrExchangeRejected      = errors.New("oauth: exchange rejected")
	ErrProviderUnavailable   = errors.New("oauth: provider unavailable")
	ErrRateLimited           = errors.New("oauth: provider rate limited")
	ErrMalformedResponse     = errors.New("oauth: malformed provider response")
	ErrResponseTooLarge      = errors.New("oauth: provider response too large")
	ErrMissingSubject        = errors.New("oauth: missing immutable subject")
)

type providerError struct {
	kind      error
	provider  string
	operation string
}

func (e *providerError) Error() string {
	return fmt.Sprintf("oauth %s %s: %s", e.provider, e.operation, e.kind)
}

func (e *providerError) Unwrap() error {
	return e.kind
}

func newProviderError(kind error, provider, operation string) error {
	return &providerError{
		kind:      kind,
		provider:  provider,
		operation: operation,
	}
}
