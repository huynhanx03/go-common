package ent

import (
	"testing"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

func TestCatalogMixinDeclaresThePersistenceContract(t *testing.T) {
	mixin := CatalogMixin{}
	fields := make(map[string]*field.Descriptor)
	for _, definition := range mixin.Fields() {
		described, ok := definition.(interface{ Descriptor() *field.Descriptor })
		if !ok {
			t.Fatalf("field %T has no Ent descriptor", definition)
		}
		fields[described.Descriptor().Name] = described.Descriptor()
	}
	for _, name := range []string{"domain", "revision", "state", "created_at", "updated_at"} {
		if fields[name] == nil {
			t.Errorf("persistence field %q is missing", name)
		}
	}
	if fields["state"].SchemaType[dialect.Postgres] != "jsonb" {
		t.Fatal("catalog state must use PostgreSQL JSONB")
	}

	indexes := mixin.Indexes()
	if len(indexes) != 1 {
		t.Fatalf("domain uniqueness index count=%d", len(indexes))
	}
	indexDescriptor, ok := indexes[0].(interface{ Descriptor() *index.Descriptor })
	if !ok || !indexDescriptor.Descriptor().Unique || len(indexDescriptor.Descriptor().Fields) != 1 || indexDescriptor.Descriptor().Fields[0] != "domain" {
		t.Fatal("catalog rows must be uniquely addressable by domain")
	}

	annotations := mixin.Annotations()
	if len(annotations) != 1 {
		t.Fatalf("table annotation count=%d", len(annotations))
	}
	annotation, ok := annotations[0].(entsql.Annotation)
	if !ok || annotation.Table != TableName || annotation.Checks["i18n_state_size"] == "" {
		t.Fatal("schema must name the catalog table and bound persisted JSONB size")
	}
}
