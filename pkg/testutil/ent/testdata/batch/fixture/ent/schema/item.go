package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Item struct{ ent.Schema }

func (Item) Fields() []ent.Field {
	return []ent.Field{
		field.String("job_key"),
		field.String("item_key"),
	}
}

func (Item) Indexes() []ent.Index {
	return []ent.Index{index.Fields("job_key", "item_key").Unique()}
}
