package ent

import (
	"context"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type (
	softDeleteKey struct{}

	// Query defines the requirements for soft-delete filtering.
	SoftDeleteQuery interface {
		WhereP(...func(*sql.Selector))
	}
)

// SoftDeleteMixin provides soft-delete fields and predicate helper.
// Hooks and Interceptors are implemented in the schema package
// where the generated client type is available.
type SoftDeleteMixin struct {
	mixin.Schema
}

// Fields adds soft-delete tracking columns.
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

// P appends the deleted_at IS NULL predicate.
func (SoftDeleteMixin) P(w SoftDeleteQuery) {
	w.WhereP(
		sql.FieldIsNull(SoftDeleteAtColumnName),
	)
}

// SkipSoftDelete returns a context that bypasses soft-delete filtering.
func SkipSoftDelete(parent context.Context) context.Context {
	return context.WithValue(parent, softDeleteKey{}, true)
}

// IsSkipSoftDelete checks if soft delete should be skipped.
func IsSkipSoftDelete(ctx context.Context) bool {
	skip, _ := ctx.Value(softDeleteKey{}).(bool)
	return skip
}
