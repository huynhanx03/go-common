package ent

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type (
	softDeleteKey struct{}

	// SoftDeleteQuery is the slice of a generated query the soft-delete
	// predicate needs — every generated query and mutation satisfies it.
	SoftDeleteQuery interface {
		WhereP(...func(*sql.Selector))
	}

	// softDeleteMutation is the slice of a generated mutation the
	// delete→update conversion needs.
	softDeleteMutation interface {
		SetOp(ent.Op)
		SetDeletedAt(time.Time)
		WhereP(...func(*sql.Selector))
	}
	deletedBySetter interface{ SetDeletedBy(string) }
)

// SoftDeleteMixin turns deletes into "set deleted_at" updates and hides
// soft-deleted rows from every query. Fields and Interceptors are fully
// generic; only the Hooks need a one-line bridge to the generated client —
// see SoftDeleteHook.
type SoftDeleteMixin struct {
	mixin.Schema
}

// Fields adds soft-delete tracking columns.
func (SoftDeleteMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time(SoftDeleteAtColumnName).
			Optional().
			Nillable(),
		field.String(SoftDeleteByColumnName).
			Optional().
			Nillable(),
	}
}

// P appends the deleted_at IS NULL predicate.
func (SoftDeleteMixin) P(w SoftDeleteQuery) {
	w.WhereP(
		sql.FieldIsNull(SoftDeleteAtColumnName),
	)
}

// Interceptors hide soft-deleted rows from every query.
func (d SoftDeleteMixin) Interceptors() []ent.Interceptor {
	return []ent.Interceptor{
		ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
			// Skip soft-delete, means include soft-deleted entities.
			if IsSkipSoftDelete(ctx) {
				return nil
			}
			if w, ok := q.(SoftDeleteQuery); ok {
				d.P(w)
			}
			return nil
		}),
	}
}

// SoftDeleteHook converts delete mutations into soft-delete updates,
// stamping deleted_at (and deleted_by when an actor resolver is registered).
// The conversion must re-enter the generated client's mutation pipeline,
// which this shared package cannot import — the application's schema mixin
// passes a one-line bridge:
//
//	type SoftDeleteMixin struct{ e.SoftDeleteMixin }
//
//	func (SoftDeleteMixin) Hooks() []ent.Hook {
//		return []ent.Hook{e.SoftDeleteHook(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
//			return m.(interface{ Client() *gen.Client }).Client().Mutate(ctx, m)
//		})}
//	}
func SoftDeleteHook(mutate func(context.Context, ent.Mutation) (ent.Value, error)) ent.Hook {
	return On(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			// Skip soft-delete, means delete the entity permanently.
			if IsSkipSoftDelete(ctx) {
				return next.Mutate(ctx, m)
			}
			mx, ok := m.(softDeleteMutation)
			if !ok {
				return nil, fmt.Errorf("ent: unexpected mutation type %T for soft delete", m)
			}
			mx.WhereP(sql.FieldIsNull(SoftDeleteAtColumnName))
			mx.SetOp(ent.OpUpdate)
			mx.SetDeletedAt(utcNow())
			if actor, ok := ActorFromContext(ctx); ok {
				if s, ok := m.(deletedBySetter); ok {
					s.SetDeletedBy(actor)
				}
			}
			return mutate(ctx, m)
		})
	}, ent.OpDeleteOne|ent.OpDelete)
}

// SkipSoftDelete returns a context that bypasses soft-delete: queries include
// soft-deleted rows and deletes remove rows permanently.
func SkipSoftDelete(parent context.Context) context.Context {
	return context.WithValue(parent, softDeleteKey{}, true)
}

// IsSkipSoftDelete checks if soft delete should be skipped.
func IsSkipSoftDelete(ctx context.Context) bool {
	skip, _ := ctx.Value(softDeleteKey{}).(bool)
	return skip
}
