package widecolumn

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

type IndexNode struct {
	Index []int
}

type StructMap struct {
	Fields map[string][]int
}

type Mapper struct {
	cache sync.Map
}

var defaultMapper = NewMapper()

func NewMapper() *Mapper {
	return &Mapper{}
}

// getStructMap loads or creates a StructMap for the given type
func (m *Mapper) getStructMap(t reflect.Type) *StructMap {
	if v, ok := m.cache.Load(t); ok {
		return v.(*StructMap)
	}

	sm := &StructMap{
		Fields: make(map[string][]int),
	}
	m.analyze(t, sm, []int{})
	m.cache.Store(t, sm)
	return sm
}

// analyze recursively scans the struct to build the field map
func (m *Mapper) analyze(t reflect.Type, sm *StructMap, basePath []int) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" && !field.Anonymous {
			continue
		}

		currentPath := make([]int, len(basePath)+1)
		copy(currentPath, basePath)
		currentPath[len(basePath)] = i

		if field.Anonymous {
			fieldType := field.Type
			if fieldType.Kind() == reflect.Ptr {
				fieldType = fieldType.Elem()
			}
			if fieldType.Kind() == reflect.Struct {
				m.analyze(fieldType, sm, currentPath)
				continue
			}
		}

		name := strings.ToLower(field.Name)
		tag := field.Tag.Get("json")
		if parts := strings.Split(tag, ","); parts[0] != "" {
			name = parts[0]
		}

		if _, exists := sm.Fields[name]; !exists {
			sm.Fields[name] = currentPath
		}
	}
}

// Bind maps a row (map[string]any) to a target struct pointer
func (m *Mapper) Bind(row map[string]any, target any) error {
	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Ptr || val.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer")
	}

	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("target must point to a struct")
	}

	sm := m.getStructMap(elem.Type())

	for colName, colVal := range row {
		if fieldPath, ok := sm.Fields[strings.ToLower(colName)]; ok {
			if err := m.setField(elem, fieldPath, colVal); err != nil {
				return err
			}
		}
	}

	return nil
}

// setField sets the value at the given index path, initializing nil pointers along the way
func (m *Mapper) setField(root reflect.Value, path []int, value any) error {
	curr := root
	for i, idx := range path {
		if curr.Kind() == reflect.Ptr {
			if curr.IsNil() {
				curr.Set(reflect.New(curr.Type().Elem()))
			}
			curr = curr.Elem()
		}

		curr = curr.Field(idx)

		if i == len(path)-1 {
			if !curr.CanSet() {
				return nil
			}

			v := reflect.ValueOf(value)
			if !v.IsValid() {
				return nil
			}

			if v.Type().ConvertibleTo(curr.Type()) {
				curr.Set(v.Convert(curr.Type()))
			} else if curr.Kind() == reflect.Ptr && v.Type().ConvertibleTo(curr.Type().Elem()) {
				ptrVal := reflect.New(curr.Type().Elem())
				ptrVal.Elem().Set(v.Convert(curr.Type().Elem()))
				curr.Set(ptrVal)
			}
		}
	}
	return nil
}
