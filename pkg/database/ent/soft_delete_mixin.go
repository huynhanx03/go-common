package ent

import (
	"context"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type (
	DeleteMutation interface {
		SetDeletedAt(time.Time)
		SetDeletedBy(any)
		SetOp(ent.Op)
	}

	softDeleteKey struct{}

	DeleteQuery interface {
		WhereP(...func(*sql.Selector))
	}
)

// SoftDeleteMixin implements the soft delete pattern for schemas.
type SoftDeleteMixin struct {
	mixin.Schema
}

// Fields of the SoftDeleteMixin.
func (SoftDeleteMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time(SoftDeleteAtColumnName).
			Optional().
			Nillable(),
		field.Int(SoftDeleteByColumnName).
			Optional().
			Nillable(),
	}
}

// Hooks of the SoftDeleteMixin.
func (d SoftDeleteMixin) Hooks() []ent.Hook {
	return []ent.Hook{
		On(
			func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if IsSkipSoftDelete(ctx) {
						return next.Mutate(ctx, m)
					}

					if deleteMutation, ok := m.(DeleteMutation); ok {
						deleteMutation.SetOp(ent.OpDelete)
						deleteMutation.SetDeletedAt(time.Now().UTC())
						// deleteMutation.SetDeletedBy(userID)
					}

					return next.Mutate(ctx, m)
				})
			},
			ent.OpDelete|ent.OpDeleteOne,
		),
	}
}

// Interceptors of the SoftDeleteMixin.
func (d SoftDeleteMixin) Interceptors() []ent.Interceptor {
	return []ent.Interceptor{
		ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
			if IsSkipSoftDelete(ctx) {
				return nil
			}

			if query, ok := q.(DeleteQuery); ok {
				d.P(query)
			}

			return nil
		}),
	}
}

func (d SoftDeleteMixin) P(w DeleteQuery) {
	w.WhereP(
		sql.FieldIsNull(d.Fields()[0].Descriptor().Name),
	)
}

// SkipSoftDelete returns a new context that skips the soft-delete interceptor/mutators.
func SkipSoftDelete(parent context.Context) context.Context {
	return context.WithValue(parent, softDeleteKey{}, true)
}

// IsSkipSoftDelete checks if soft delete should be skipped.
func IsSkipSoftDelete(ctx context.Context) bool {
	skip, _ := ctx.Value(softDeleteKey{}).(bool)
	return skip
}
