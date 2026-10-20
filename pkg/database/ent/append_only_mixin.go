package ent

import (
	"context"
	"errors"

	"entgo.io/ent"
	"entgo.io/ent/schema/mixin"
)

// ErrAppendOnlyMutation reports an attempted non-create mutation on a schema
// protected by AppendOnlyMixin.
var ErrAppendOnlyMutation = errors.New("ent: append-only schema accepts create mutations only")

// AppendOnlyMixin rejects update and delete mutations at the generated Ent
// client boundary. It deliberately permits only an exact OpCreate value, so
// nil, combined, unknown, and future mutation operations fail closed.
//
// This is application-level defense in depth. A database trigger is still
// required when append-only is a durable invariant: raw SQL bypasses hooks,
// and Ent presents create builders configured with ON CONFLICT DO UPDATE as
// OpCreate, which this generic hook cannot distinguish from a plain insert.
type AppendOnlyMixin struct {
	mixin.Schema
}

// Hooks permits creates and rejects every other mutation operation.
func (AppendOnlyMixin) Hooks() []ent.Hook {
	return []ent.Hook{
		func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				if mutation == nil || mutation.Op() != ent.OpCreate {
					return nil, ErrAppendOnlyMutation
				}
				return next.Mutate(ctx, mutation)
			})
		},
	}
}
