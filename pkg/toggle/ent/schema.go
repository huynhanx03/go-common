// Package ent provides PostgreSQL persistence for toggle using a caller-owned
// Ent SQL driver. Applications own migrations and database lifecycle.
package ent

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"entgo.io/ent/schema/mixin"
)

// ScopeMixin provides the canonical fields required by the PostgreSQL Store.
// Add it to one service-owned Ent schema and annotate that schema with table
// name toggle_scopes.
type ScopeMixin struct{ mixin.Schema }

func (ScopeMixin) Fields() []ent.Field {
	return []ent.Field{
		field.String("namespace").NotEmpty().MaxLen(100),
		field.String("environment").NotEmpty().MaxLen(100),
		field.Int64("revision").NonNegative().Default(0),
	}
}

func (ScopeMixin) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Checks: map[string]string{"toggle_revision_nonnegative": "revision >= 0"}}}
}

func (ScopeMixin) Indexes() []ent.Index {
	return []ent.Index{index.Fields("namespace", "environment").Unique()}
}

// FlagMixin provides the canonical definition columns required by the Store.
// Add it to one service-owned Ent schema and annotate that schema with table
// name toggle_flags.
type FlagMixin struct{ mixin.Schema }

func (FlagMixin) Fields() []ent.Field {
	return []ent.Field{
		field.String("namespace").NotEmpty().MaxLen(100),
		field.String("environment").NotEmpty().MaxLen(100),
		field.String("key").NotEmpty().MaxLen(100),
		field.Bool("enabled").Default(false),
		field.String("description").Default("").MaxLen(4096),
		field.JSON("definition", json.RawMessage{}).SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Time("created_at").Default(time.Now).Immutable().Annotations(entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).Annotations(entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}),
	}
}

func (FlagMixin) Indexes() []ent.Index {
	return []ent.Index{index.Fields("namespace", "environment", "key").Unique()}
}
