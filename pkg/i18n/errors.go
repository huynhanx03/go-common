package i18n

import "errors"

var (
	ErrInvalidConfig     = errors.New("invalid configuration")
	ErrInvalidDefinition = errors.New("invalid definition")
	ErrInvalidParams     = errors.New("invalid parameters")
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("revision conflict")
	ErrIncomplete        = errors.New("catalog incomplete")
	ErrLimit             = errors.New("resource limit exceeded")
	ErrSource            = errors.New("source unavailable")
	ErrStaleRevision     = errors.New("stale revision")
	ErrClosed            = errors.New("client closed")
	ErrInvalidation      = errors.New("catalog change committed but invalidation failed")
)

// CommittedError means persistence succeeded. Retry invalidation, not the write.
type CommittedError struct {
	Revision int64
	cause    error
}

func (e *CommittedError) Error() string   { return ErrInvalidation.Error() }
func (e *CommittedError) Unwrap() []error { return []error{ErrInvalidation, e.cause} }

type failure struct{ category, cause error }

func (e failure) Error() string   { return e.category.Error() }
func (e failure) Unwrap() []error { return []error{e.category, e.cause} }
func sourceError(err error) error { return failure{ErrSource, err} }
