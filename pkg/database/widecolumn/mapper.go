package widecolumn

import (
	"fmt"
	"math"
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
	m.analyze(t, sm, nil, make(map[reflect.Type]bool))
	m.cache.Store(t, sm)
	return sm
}

// analyze recursively scans the struct to build the field map
func (m *Mapper) analyze(
	t reflect.Type,
	sm *StructMap,
	basePath []int,
	visiting map[reflect.Type]bool,
) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || visiting[t] {
		return
	}
	visiting[t] = true
	defer delete(visiting, t)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
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
				m.analyze(fieldType, sm, currentPath, visiting)
				continue
			}
		}

		name, include := mappedFieldName(field)
		if !include {
			continue
		}
		if _, exists := sm.Fields[name]; !exists {
			sm.Fields[name] = currentPath
		}
	}
}

// Bind maps a row (map[string]any) to a target struct pointer
func (m *Mapper) Bind(row map[string]any, target any) error {
	if m == nil {
		return fmt.Errorf("mapper must be initialized")
	}
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
				return fmt.Errorf("map column %q: %w", colName, err)
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
				return fmt.Errorf("target field is not settable")
			}

			if value == nil {
				if isNilableKind(curr.Kind()) {
					curr.SetZero()
					return nil
				}
				return fmt.Errorf("cannot assign null to %s", curr.Type())
			}

			v := reflect.ValueOf(value)
			if assigned, ok := assignValue(v, curr.Type()); ok {
				curr.Set(assigned)
				return nil
			}
			if curr.Kind() == reflect.Ptr {
				if assigned, ok := assignValue(v, curr.Type().Elem()); ok {
					pointer := reflect.New(curr.Type().Elem())
					pointer.Elem().Set(assigned)
					curr.Set(pointer)
					return nil
				}
			}
			return fmt.Errorf("cannot assign %s to %s", v.Type(), curr.Type())
		}
	}
	return nil
}

func mappedFieldName(field reflect.StructField) (string, bool) {
	for _, tagName := range []string{"cql", "db", "json"} {
		tag := field.Tag.Get(tagName)
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			return "", false
		}
		if name != "" {
			return strings.ToLower(name), true
		}
	}
	return strings.ToLower(field.Name), true
}

func assignValue(value reflect.Value, target reflect.Type) (reflect.Value, bool) {
	if value.Type().AssignableTo(target) {
		return value, true
	}
	if !value.Type().ConvertibleTo(target) || !safeNumericConversion(value, target) {
		return reflect.Value{}, false
	}
	return value.Convert(target), true
}

func safeNumericConversion(value reflect.Value, target reflect.Type) bool {
	sourceKind := value.Kind()
	targetKind := target.Kind()

	if isSignedInteger(targetKind) {
		targetValue := reflect.New(target).Elem()
		switch {
		case isSignedInteger(sourceKind):
			return !targetValue.OverflowInt(value.Int())
		case isUnsignedInteger(sourceKind):
			unsigned := value.Uint()
			if unsigned > uint64(math.MaxInt64) {
				return false
			}
			return !targetValue.OverflowInt(int64(unsigned))
		default:
			return false
		}
	}
	if isUnsignedInteger(targetKind) {
		targetValue := reflect.New(target).Elem()
		switch {
		case isUnsignedInteger(sourceKind):
			return !targetValue.OverflowUint(value.Uint())
		case isSignedInteger(sourceKind):
			signed := value.Int()
			return signed >= 0 && !targetValue.OverflowUint(uint64(signed))
		default:
			return false
		}
	}
	if isFloat(targetKind) {
		if !isFloat(sourceKind) {
			return false
		}
		return !reflect.New(target).Elem().OverflowFloat(value.Float())
	}
	if isNumeric(sourceKind) {
		return false
	}
	return true
}

func isSignedInteger(kind reflect.Kind) bool {
	return kind >= reflect.Int && kind <= reflect.Int64
}

func isUnsignedInteger(kind reflect.Kind) bool {
	return kind >= reflect.Uint && kind <= reflect.Uintptr
}

func isFloat(kind reflect.Kind) bool {
	return kind == reflect.Float32 || kind == reflect.Float64
}

func isNumeric(kind reflect.Kind) bool {
	return isSignedInteger(kind) || isUnsignedInteger(kind) || isFloat(kind) ||
		kind == reflect.Complex64 || kind == reflect.Complex128
}

func isNilableKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
