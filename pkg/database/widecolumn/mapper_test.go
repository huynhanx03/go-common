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
