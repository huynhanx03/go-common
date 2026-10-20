package widecolumn

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type NestedStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type ComplexStruct struct {
	*NestedStruct
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type SimpleStruct struct {
	Title string
	Value int
}

func TestMapper_Bind_Simple(t *testing.T) {
	m := NewMapper()
	row := map[string]any{
		"title": "Hello",
		"value": 123,
	}

	var target SimpleStruct
	err := m.Bind(row, &target)

	assert.NoError(t, err)
	assert.Equal(t, "Hello", target.Title)
	assert.Equal(t, 123, target.Value)
}

func TestMapper_Bind_Nested_InitNil(t *testing.T) {
	m := NewMapper()
	now := time.Now()
	row := map[string]any{
		"id":         "uuid-123",
		"created_at": now,
		"name":       "GoLink",
		"age":        1,
	}

	var target ComplexStruct
	// Ensure NestedStruct is nil initially
	assert.Nil(t, target.NestedStruct)

	err := m.Bind(row, &target)

	assert.NoError(t, err)
	assert.Equal(t, "uuid-123", target.ID)
	assert.Equal(t, now, target.CreatedAt)

	// Verified Auto-Init
	assert.NotNil(t, target.NestedStruct)
	assert.Equal(t, "GoLink", target.Name)
	assert.Equal(t, 1, target.Age)
}

func TestMapper_Bind_ExtraColumns(t *testing.T) {
	m := NewMapper()
	row := map[string]any{
		"title": "Hello",
		"extra": "ignored",
	}

	var target SimpleStruct
	err := m.Bind(row, &target)

	assert.NoError(t, err)
	assert.Equal(t, "Hello", target.Title)
}

func TestMapper_Bind_CaseInsensitive(t *testing.T) {
	m := NewMapper()
	row := map[string]any{
		"TiTlE": "Hello",
	}

	var target SimpleStruct
	err := m.Bind(row, &target)

	assert.NoError(t, err)
	assert.Equal(t, "Hello", target.Title)
}

func TestMapper_Bind_RejectsIncompatibleMappedValue(t *testing.T) {
	m := NewMapper()
	row := map[string]any{
		"value": "not-an-integer",
	}

	var target SimpleStruct
	err := m.Bind(row, &target)

	assert.ErrorContains(t, err, `map column "value"`)
}

func TestMapper_Bind_RejectsOverflow(t *testing.T) {
	type narrow struct {
		Value int8 `cql:"value"`
	}
	m := NewMapper()
	row := map[string]any{
		"value": int64(128),
	}

	var target narrow
	err := m.Bind(row, &target)

	assert.Error(t, err)
	assert.Zero(t, target.Value)
}

func TestMapper_Bind_RejectsNullForValueField(t *testing.T) {
	m := NewMapper()
	row := map[string]any{
		"value": nil,
	}

	var target SimpleStruct
	err := m.Bind(row, &target)

	assert.Error(t, err)
}

func TestMapper_Bind_AllowsNullForPointerField(t *testing.T) {
	type nullable struct {
		Value *int `cql:"value"`
	}
	m := NewMapper()
	value := 42
	target := nullable{Value: &value}

	err := m.Bind(map[string]any{"value": nil}, &target)

	assert.NoError(t, err)
	assert.Nil(t, target.Value)
}

func TestMapper_Bind_PrefersCQLTagAndIgnoresExcludedFields(t *testing.T) {
	type tagged struct {
		Value   string `cql:"stored_value" json:"value"`
		Ignored string `cql:"-" json:"ignored"`
	}
	m := NewMapper()
	row := map[string]any{
		"stored_value": "mapped",
		"ignored":      "must-not-map",
	}

	var target tagged
	err := m.Bind(row, &target)

	assert.NoError(t, err)
	assert.Equal(t, "mapped", target.Value)
	assert.Empty(t, target.Ignored)
}
