package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type Ledger struct{ ent.Schema }

func (Ledger) Fields() []ent.Field {
	return []ent.Field{
		field.String("job_key"),
		field.Int64("processed"),
	}
}
