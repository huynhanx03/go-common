package ent

import (
	"context"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type (
	skipModifierKey struct{}

	// createdBySetter / updatedBySetter are the slices of a generated
	// mutation the modifier hook needs — duck-typed so this package never
	// imports generated code.
	createdBySetter interface{ SetCreatedBy(string) }
	updatedBySetter interface{ SetUpdatedBy(string) }
)

// ModifierMixin adds created_by / updated_by columns and stamps them
// automatically from the actor registered via SetActorResolver. A value the
// mutation already set explicitly always wins.
type ModifierMixin struct {
	mixin.Schema
}

// Fields of the ModifierMixin.
func (ModifierMixin) Fields() []ent.Field {
	return []ent.Field{
		field.String(CreatedByColumnName).
			Optional().
			Immutable(),
		field.String(UpdatedByColumnName).
			Optional().
			Nillable(),
	}
}

// Hooks stamp the actor onto create and update mutations.
func (ModifierMixin) Hooks() []ent.Hook {
	return []ent.Hook{
		On(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if !IsSkipModifier(ctx) {
					stampActor(ctx, m)
				}
				return next.Mutate(ctx, m)
			})
		}, ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne),
	}
}

// stampActor writes the context's actor into the mutation, respecting
// explicitly set values.
func stampActor(ctx context.Context, m ent.Mutation) {
	actor, ok := ActorFromContext(ctx)
	if !ok {
		return
	}

	switch {
	case m.Op().Is(ent.OpCreate):
		if s, ok := m.(createdBySetter); ok {
			if _, set := m.Field(CreatedByColumnName); !set {
				s.SetCreatedBy(actor)
			}
		}
	case m.Op().Is(ent.OpUpdate | ent.OpUpdateOne):
		if s, ok := m.(updatedBySetter); ok {
			if _, set := m.Field(UpdatedByColumnName); !set {
				s.SetUpdatedBy(actor)
			}
		}
	}
}

// SkipModifier returns a context whose mutations bypass actor stamping
// (data imports, system migrations).
func SkipModifier(parent context.Context) context.Context {
	return context.WithValue(parent, skipModifierKey{}, true)
}

// IsSkipModifier reports whether actor stamping should be skipped.
func IsSkipModifier(ctx context.Context) bool {
	skip, _ := ctx.Value(skipModifierKey{}).(bool)
	return skip
}
