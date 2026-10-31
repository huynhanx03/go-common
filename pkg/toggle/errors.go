package toggle

import "errors"

var (
	ErrInvalidConfig     = errors.New("toggle: invalid configuration")
	ErrInvalidDefinition = errors.New("toggle: invalid definition")
	ErrInvalidContext    = errors.New("toggle: invalid evaluation context")
	ErrLimit             = errors.New("toggle: limit exceeded")
	ErrNotFound          = errors.New("toggle: flag not found")
	ErrConflict          = errors.New("toggle: revision conflict")
	ErrStaleRevision     = errors.New("toggle: stale revision")
	ErrClosed            = errors.New("toggle: client closed")
	ErrSource            = errors.New("toggle: source failed")
)

// categorizedError keeps error text independent of user-provided causes.
type categorizedError struct {
	category error
	cause    error
}

func (e categorizedError) Error() string   { return e.category.Error() }
func (e categorizedError) Unwrap() []error { return []error{e.category, e.cause} }
func invalidLimit(category error) error    { return categorizedError{category, ErrLimit} }

type operationError struct {
	operation string
	cause     error
}

func (e operationError) Error() string { return e.operation }
func (e operationError) Unwrap() error { return e.cause }
