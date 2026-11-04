// Package ent provides PostgreSQL persistence and an Ent schema mixin for i18n.
package ent

import (
	"strconv"
	"time"

	entgo "entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"entgo.io/ent/schema/mixin"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/i18n"
)

// TableName is the table name queried by Store. Add CatalogMixin to a
// service-owned Ent schema and annotate that schema with this table name.
const TableName = "i18n_catalogs"

// MaxJSONBTextBytes bounds PostgreSQL's expanded JSONB text representation;
// NormalizeCatalog separately enforces the compact 16 MiB catalog document.
const MaxJSONBTextBytes = i18n.MaxCatalogBytes * 2

// CatalogMixin describes one domain's draft and last published snapshot.
// Keeping both in one row makes publishing an atomic compare-and-swap.
type CatalogMixin struct{ mixin.Schema }

func (CatalogMixin) Fields() []entgo.Field {
	return []entgo.Field{
		field.String("domain").NotEmpty().MaxLen(i18n.MaxIdentifierBytes),
		field.Int64("revision").Positive(),
		field.JSON("state", jsoncodec.RawMessage{}).SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Time("created_at").Default(time.Now).Immutable().Annotations(entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).Annotations(entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}),
	}
}

func (CatalogMixin) Indexes() []entgo.Index {
	return []entgo.Index{index.Fields("domain").Unique()}
}

func (CatalogMixin) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: TableName, Checks: map[string]string{
		"i18n_state_size": "octet_length(state::text) <= " + strconv.Itoa(MaxJSONBTextBytes),
	}}}
}
