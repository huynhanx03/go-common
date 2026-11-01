package ent

import (
	entgo "entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"entgo.io/ent/schema/mixin"
)

// Mixin defines the canonical Ent schema for Casbin policy rows.
type Mixin struct{ mixin.Schema }

func (Mixin) Fields() []entgo.Field {
	return []entgo.Field{
		field.String("ptype").Default(""),
		field.String("v0").Default(""),
		field.String("v1").Default(""),
		field.String("v2").Default(""),
		field.String("v3").Default(""),
		field.String("v4").Default(""),
		field.String("v5").Default(""),
	}
}

func (Mixin) Indexes() []entgo.Index {
	return []entgo.Index{
		index.Fields("ptype", "v0", "v1", "v2", "v3", "v4", "v5").Unique(),
		index.Fields("ptype", "v1"), // Scope-filtered permission policies.
		index.Fields("ptype", "v2"), // Scope-filtered role and object relations.
	}
}
