package toggle

import "github.com/huynhanx03/go-common/pkg/toggle/core"

var (
	ErrInvalidConfig     = core.ErrInvalidConfig
	ErrInvalidDefinition = core.ErrInvalidDefinition
	ErrInvalidContext    = core.ErrInvalidContext
	ErrLimit             = core.ErrLimit
	ErrNotFound          = core.ErrNotFound
	ErrConflict          = core.ErrConflict
	ErrStaleRevision     = core.ErrStaleRevision
	ErrClosed            = core.ErrClosed
	ErrSource            = core.ErrSource
)

type operationError struct {
	operation string
	cause     error
}

func (e operationError) Error() string { return e.operation }
func (e operationError) Unwrap() error { return e.cause }
