package batch

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/coordination"
)

// Store owns durable checkpoint and business-row transactions for one adapter.
// Apply must validate ownership while holding the checkpoint row lock, invoke
// Step in the same transaction, and write the checkpoint receipt atomically.
type Store interface {
	coordination.FencedAdapter
	Open(context.Context, Definition) (Checkpoint, error)
	Get(context.Context, string) (Checkpoint, error)
	Apply(context.Context, StepRequest, Step) (Checkpoint, error)
}
