package core

import "errors"

var (
	ErrInvalidConfig     = errors.New("invalid configuration")
	ErrInvalidDefinition = errors.New("invalid definition")
	ErrInvalidContext    = errors.New("invalid evaluation context")
	ErrLimit             = errors.New("limit exceeded")
	ErrNotFound          = errors.New("flag not found")
	ErrConflict          = errors.New("revision conflict")
	ErrStaleRevision     = errors.New("stale revision")
	ErrClosed            = errors.New("client closed")
	ErrSource            = errors.New("source failed")
)

type categorizedError struct {
	category error
	cause    error
}

func (e categorizedError) Error() string   { return e.category.Error() }
func (e categorizedError) Unwrap() []error { return []error{e.category, e.cause} }

// Wrap preserves a stable error category while retaining its cause for errors.Is.
func Wrap(category, cause error) error {
	if category == nil {
		return cause
	}
	return categorizedError{category: category, cause: cause}
}

// SourceError classifies a source failure without including backend details in Error().
func SourceError(cause error) error { return Wrap(ErrSource, cause) }

// LimitError marks a validation category as exceeding a configured limit.
func LimitError(category error) error { return Wrap(category, ErrLimit) }

func invalidLimit(category error) error {
	return LimitError(category)
}
