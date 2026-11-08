// Package ent provides the shared Ent schema fields for an inbox table.
package ent

import (
	"time"

	entgo "entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"entgo.io/ent/schema/mixin"

	"github.com/huynhanx03/go-common/pkg/mq/inbox"
)

// Mixin defines the shared columns and indexes for an inbox entity.
type Mixin struct{ mixin.Schema }

func (Mixin) Fields() []entgo.Field {
	return []entgo.Field{
		field.String(inbox.ConsumerColumn).NotEmpty(),
		field.String(inbox.MessageIDColumn).NotEmpty(),
		field.Time(inbox.CreatedAtColumn).
			Default(func() time.Time { return time.Now().UTC() }).
			Immutable().
			Annotations(entsql.Default("CURRENT_TIMESTAMP")),
	}
}

func (Mixin) Indexes() []entgo.Index {
	return []entgo.Index{
		index.Fields(inbox.ConsumerColumn, inbox.MessageIDColumn).Unique(),
		index.Fields(inbox.CreatedAtColumn),
	}
}

func (Mixin) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: inbox.TableName}}
}
