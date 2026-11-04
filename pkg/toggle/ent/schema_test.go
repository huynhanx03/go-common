package ent_test

import (
	"reflect"
	"testing"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
)

func TestEntMixinsExposeColumnsAndConstraintsRequiredByStore(t *testing.T) {
	scopeMixin := toggleent.ScopeMixin{}
	scopeFields := descriptors(scopeMixin.Fields())
	if len(scopeFields) != 3 || scopeFields["namespace"] == nil || scopeFields["environment"] == nil || scopeFields["revision"] == nil {
		t.Fatalf("scope mixin fields do not satisfy the Store contract: %v", fieldNames(scopeFields))
	}
	for _, name := range []string{"namespace", "environment"} {
		if scopeFields[name].Size != core.MaxIdentifierLength || scopeFields[name].Optional {
			t.Errorf("scope field %q has size=%d optional=%v", name, scopeFields[name].Size, scopeFields[name].Optional)
		}
	}
	if scopeFields["revision"].Default != int64(0) {
		t.Errorf("scope revision default = %v, want zero", scopeFields["revision"].Default)
	}
	if len(scopeMixin.Indexes()) != 1 || !scopeMixin.Indexes()[0].Descriptor().Unique || !reflect.DeepEqual(scopeMixin.Indexes()[0].Descriptor().Fields, []string{"namespace", "environment"}) {
		t.Fatalf("scope index does not enforce one revision per scope")
	}
	annotations := scopeMixin.Annotations()
	if len(annotations) != 1 {
		t.Fatalf("scope annotations = %#v", annotations)
	}
	annotation, ok := annotations[0].(entsql.Annotation)
	if !ok || len(annotation.Checks) != 1 {
		t.Fatalf("scope revision check is missing: %#v", annotations)
	}

	flagMixin := toggleent.FlagMixin{}
	flagFields := descriptors(flagMixin.Fields())
	want := []string{"namespace", "environment", "key", "enabled", "description", "definition", "created_at", "updated_at"}
	if len(flagFields) != len(want) {
		t.Fatalf("flag mixin has %v, want store columns %v", fieldNames(flagFields), want)
	}
	for _, name := range want {
		if flagFields[name] == nil {
			t.Errorf("flag mixin is missing %q", name)
		}
	}
	for _, name := range []string{"namespace", "environment", "key"} {
		if flagFields[name].Size != core.MaxIdentifierLength || flagFields[name].Optional {
			t.Errorf("flag field %q has size=%d optional=%v", name, flagFields[name].Size, flagFields[name].Optional)
		}
	}
	if flagFields["description"].Size != core.MaxFlagDescriptionLength || flagFields["description"].Default != "" {
		t.Errorf("description field does not match the Store limits/default: %+v", flagFields["description"])
	}
	if flagFields["definition"].SchemaType[dialect.Postgres] != "jsonb" {
		t.Errorf("definition PostgreSQL type = %q, want jsonb", flagFields["definition"].SchemaType[dialect.Postgres])
	}
	if !flagFields["created_at"].Immutable || flagFields["updated_at"].UpdateDefault == nil {
		t.Fatal("creation/update timestamps do not preserve their lifecycle semantics")
	}
	indexes := flagMixin.Indexes()
	if len(indexes) != 1 || !indexes[0].Descriptor().Unique || !reflect.DeepEqual(indexes[0].Descriptor().Fields, []string{"namespace", "environment", "key"}) {
		t.Fatal("flag index does not prevent duplicate keys within a scope")
	}
}

func descriptors(fields []ent.Field) map[string]*field.Descriptor {
	result := make(map[string]*field.Descriptor, len(fields))
	for _, field := range fields {
		descriptor := field.Descriptor()
		result[descriptor.Name] = descriptor
	}
	return result
}

func fieldNames(fields map[string]*field.Descriptor) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	return names
}
