package ent

import (
	"fmt"
	"reflect"
	"time"

	"github.com/huynhanx03/go-common/pkg/constraints"
)

type fieldInfo struct {
	Index      int
	Name       string
	SetterName string
	IsTime     bool
}

type entityMetadata struct {
	Typ           reflect.Type
	EntityName    string
	ClientIndex   int
	MethodCreate  int
	MethodUpdate  int
	MethodDelete  int
	MethodGet     int
	MethodQuery   int
	MethodMapBulk int
	Fields        []fieldInfo
}

func newEntityMetadata[T any, ID constraints.ID](client any) (*entityMetadata, error) {
	var zero T
	typ := reflect.TypeOf(zero)

	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("T must be a struct or pointer to struct, got %s", typ.Kind())
	}

	entityName := typ.Name()
	meta := &entityMetadata{
		Typ:        typ,
		EntityName: entityName,
	}

	clientVal := reflect.ValueOf(client).Elem()
	clientTyp := clientVal.Type()
	clientField, ok := clientTyp.FieldByName(entityName)
	if !ok {
		return nil, fmt.Errorf("entity client field not found for %s", entityName)
	}
	meta.ClientIndex = clientField.Index[0]

	specificClientVal := clientVal.Field(meta.ClientIndex)
	specificClientTyp := specificClientVal.Type()

	findMethodIndex := func(methodName string) (int, error) {
		m, ok := specificClientTyp.MethodByName(methodName)
		if !ok {
			return -1, fmt.Errorf("method %s not found for entity %s", methodName, entityName)
		}
		return m.Index, nil
	}

	var err error
	if meta.MethodCreate, err = findMethodIndex(MethodCreate); err != nil {
		return nil, err
	}
	if meta.MethodUpdate, err = findMethodIndex(MethodUpdateOneID); err != nil {
		return nil, err
	}
	if meta.MethodDelete, err = findMethodIndex(MethodDeleteOneID); err != nil {
		return nil, err
	}
	if meta.MethodGet, err = findMethodIndex(MethodGet); err != nil {
		return nil, err
	}
	if meta.MethodQuery, err = findMethodIndex(MethodQuery); err != nil {
		return nil, err
	}

	if m, ok := specificClientTyp.MethodByName(MethodMapCreateBulk); ok {
		meta.MethodMapBulk = m.Index
	} else {
		meta.MethodMapBulk = -1
	}

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		if field.PkgPath != "" ||
			field.Name == FieldID ||
			field.Name == FieldEdges ||
			field.Name == FieldConfig {
			continue
		}

		fInfo := fieldInfo{
			Index:      i,
			Name:       field.Name,
			SetterName: PrefixSet + field.Name,
			IsTime:     field.Type == reflect.TypeOf(time.Time{}),
		}
		meta.Fields = append(meta.Fields, fInfo)
	}

	return meta, nil
}
