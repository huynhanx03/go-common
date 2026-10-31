package toggle

import "context"

// Source loads one complete consistent snapshot. Implementations must honor ctx.
type Source interface {
	Load(context.Context, Scope) (Snapshot, error)
}
