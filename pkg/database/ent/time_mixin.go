package ent

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// utcNow keeps every timestamp the mixins write in UTC, so rows compare
// correctly across app instances in different timezones.
func utcNow() time.Time { return time.Now().UTC() }

// TimeMixin defines the common time fields (created_at, updated_at) for
// schemas. Timestamps are UTC; created_at also gets a database-side
// CURRENT_TIMESTAMP default so rows inserted outside the app (seeds,
// migrations, manual SQL) are stamped too.
type TimeMixin struct {
	mixin.Schema
}

// Fields of the TimeMixin.
func (TimeMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time(CreatedAtColumnName).
			Default(utcNow).
			Immutable().
			Annotations(entsql.Default("CURRENT_TIMESTAMP")),
		field.Time(UpdatedAtColumnName).
			Default(utcNow).
			UpdateDefault(utcNow).
			Annotations(entsql.Default("CURRENT_TIMESTAMP")),
	}
}
