package schema

import (
	"entgo.io/ent"

	batchent "github.com/huynhanx03/go-common/pkg/workflow/batch/ent"
)

type Checkpoint struct{ ent.Schema }

func (Checkpoint) Mixin() []ent.Mixin { return []ent.Mixin{batchent.Mixin{}} }
