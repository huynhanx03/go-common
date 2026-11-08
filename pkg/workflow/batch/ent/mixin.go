package ent

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"entgo.io/ent/schema/mixin"
)

// Mixin defines the durable state required by a batch.Store implementation.
// Applications embed it in their own Ent schema and keep business data local.
type Mixin struct{ mixin.Schema }

func (Mixin) Fields() []ent.Field {
	return []ent.Field{
		field.String("job_key").NotEmpty().Unique(),
		field.String("input_fingerprint").NotEmpty(),
		field.String("cursor_schema").NotEmpty(),
		field.Bytes("boundary").Default([]byte{}),
		field.Bytes("cursor").Default([]byte{}),
		field.String("status").NotEmpty(),
		field.Int64("version").Default(0),
		field.String("last_step_id").Default(""),
		field.Int64("processed_count").Default(0),
		field.String("owner").Default(""),
		field.String("fence_authority").NotEmpty(),
		field.Int64("fence").Default(0),
		field.Time("lease_until"),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
		field.Time("completed_at").Optional().Nillable(),
	}
}

func (Mixin) Indexes() []ent.Index {
	return []ent.Index{index.Fields("status", "lease_until")}
}

func (Mixin) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Checks(map[string]string{
		"batch_version_nonnegative":         "version >= 0",
		"batch_processed_count_nonnegative": "processed_count >= 0",
		"batch_fence_nonnegative":           "fence >= 0",
	})}
}
