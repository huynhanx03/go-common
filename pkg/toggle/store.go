package toggle

import "context"

// Store persists complete definitions using scope-level revision preconditions.
// Implementations must validate direct calls and return owned snapshots.
type Store interface {
	Source
	Put(context.Context, Scope, Flag, int64) (int64, error)
	Delete(context.Context, Scope, string, int64) (int64, error)
}
