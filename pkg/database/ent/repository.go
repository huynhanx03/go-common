package ent

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/dto"
)

// BaseRepository implements generic repository operations for Ent.
type BaseRepository[T any, PT interface {
	*T
	Model[ID]
}, ID constraints.ID] struct {
	client any
	meta   *entityMetadata
}

// NewBaseRepository creates a new repository and warms up reflection metadata.
func NewBaseRepository[T any, PT interface {
	*T
	Model[ID]
}, ID constraints.ID](client any) *BaseRepository[T, PT, ID] {
	meta, err := newEntityMetadata[T, ID](client)
	if err != nil {
		panic(fmt.Errorf("failed to initialize repository metadata: %w", err))
	}

	return &BaseRepository[T, PT, ID]{
		client: client,
		meta:   meta,
	}
}

func (r *BaseRepository[T, PT, ID]) getEntityClient() reflect.Value {
	return reflect.ValueOf(r.client).Elem().Field(r.meta.ClientIndex)
}

func (r *BaseRepository[T, PT, ID]) setFields(builder reflect.Value, modelVal reflect.Value) {
	for _, fieldInfo := range r.meta.Fields {
		fieldVal := modelVal.Field(fieldInfo.Index)

		if fieldInfo.IsTime && fieldVal.Interface().(time.Time).IsZero() {
			continue
		}

		setter := builder.MethodByName(fieldInfo.SetterName)
		if setter.IsValid() {
			setter.Call([]reflect.Value{fieldVal})
		}
	}
}

// Create inserts a new model.
func (r *BaseRepository[T, PT, ID]) Create(ctx context.Context, model *T) error {
	val := reflect.ValueOf(model).Elem()
	client := r.getEntityClient()

	createMethod := client.Method(r.meta.MethodCreate)
	createBuilder := createMethod.Call(nil)[0]

	r.setFields(createBuilder, val)

	saveMethod := createBuilder.MethodByName(MethodSave)
	results := saveMethod.Call([]reflect.Value{reflect.ValueOf(ctx)})
	if errVal := results[1]; !errVal.IsNil() {
		return errVal.Interface().(error)
	}

	savedModel := results[0].Elem()
	val.Set(savedModel)

	return nil
}

// Update updates an existing model.
func (r *BaseRepository[T, PT, ID]) Update(ctx context.Context, model *T) error {
	val := reflect.ValueOf(model).Elem()
	client := r.getEntityClient()

	// Cast to PT to access GetID
	idValue := reflect.ValueOf(PT(model).GetID())

	updateMethod := client.Method(r.meta.MethodUpdate)
	updateBuilder := updateMethod.Call([]reflect.Value{idValue})[0]

	r.setFields(updateBuilder, val)

	saveMethod := updateBuilder.MethodByName(MethodSave)
	results := saveMethod.Call([]reflect.Value{reflect.ValueOf(ctx)})
	if errVal := results[1]; !errVal.IsNil() {
		return errVal.Interface().(error)
	}

	savedModel := results[0].Elem()
	val.Set(savedModel)

	return nil
}

// Delete removes a model by ID.
func (r *BaseRepository[T, PT, ID]) Delete(ctx context.Context, id ID) error {
	client := r.getEntityClient()

	deleteMethod := client.Method(r.meta.MethodDelete)
	deleteBuilder := deleteMethod.Call([]reflect.Value{reflect.ValueOf(id)})[0]
	execMethod := deleteBuilder.MethodByName(MethodExec)

	results := execMethod.Call([]reflect.Value{reflect.ValueOf(ctx)})
	if errVal := results[0]; !errVal.IsNil() {
		return errVal.Interface().(error)
	}

	return nil
}

// Get retrieves a model by ID.
func (r *BaseRepository[T, PT, ID]) Get(ctx context.Context, id ID) (*T, error) {
	client := r.getEntityClient()

	getMethod := client.Method(r.meta.MethodGet)
	results := getMethod.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(id)})

	if errVal := results[1]; !errVal.IsNil() {
		return nil, errVal.Interface().(error)
	}

	result := results[0].Interface().(*T)
	return result, nil
}

// Find retrieves a paginated list of models.
func (r *BaseRepository[T, PT, ID]) Find(ctx context.Context, opts *dto.QueryOptions) (*dto.Paginated[*T], error) {
	client := r.getEntityClient()

	queryMethod := client.Method(r.meta.MethodQuery)
	queryBuilder := queryMethod.Call(nil)[0]

	queryBuilder = ApplyQueryOptions(queryBuilder, opts)

	countBuilder := client.Method(r.meta.MethodQuery).Call(nil)[0]
	countResult := countBuilder.MethodByName(MethodCount).Call([]reflect.Value{reflect.ValueOf(ctx)})
	if errVal := countResult[1]; !errVal.IsNil() {
		return nil, errVal.Interface().(error)
	}
	totalItems := int64(countResult[0].Int())

	allResult := queryBuilder.MethodByName(MethodAll).Call([]reflect.Value{reflect.ValueOf(ctx)})
	if errVal := allResult[1]; !errVal.IsNil() {
		return nil, errVal.Interface().(error)
	}

	resultSlice := allResult[0]
	records := make([]*T, resultSlice.Len())
	for i := 0; i < resultSlice.Len(); i++ {
		records[i] = resultSlice.Index(i).Interface().(*T)
	}

	pagination := dto.CalculatePagination(opts.Pagination.Page, opts.Pagination.PageSize, totalItems)

	return &dto.Paginated[*T]{
		Records:    &records,
		Pagination: pagination,
	}, nil
}

// IsNotFound checks if the error is an Ent "not found" error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}

	return err.Error() == "ent: not found" || reflect.TypeOf(err).Name() == "NotFoundError"
}

// Exists checks if a model exists by ID.
func (r *BaseRepository[T, PT, ID]) Exists(ctx context.Context, id ID) (bool, error) {
	_, err := r.Get(ctx, id)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// BatchCreate inserts multiple models.
func (r *BaseRepository[T, PT, ID]) BatchCreate(ctx context.Context, models []*T) error {
	if len(models) == 0 {
		return nil
	}
	if r.meta.MethodMapBulk == -1 {
		return fmt.Errorf("MapCreateBulk method not supported for %s", r.meta.EntityName)
	}

	client := r.getEntityClient()
	mapCreateBulkMethod := client.Method(r.meta.MethodMapBulk)
	builderType := client.Method(r.meta.MethodCreate).Type().Out(0)

	setFuncType := reflect.FuncOf(
		[]reflect.Type{builderType, reflect.TypeOf(int(0))},
		[]reflect.Type{},
		false,
	)

	modelsVal := reflect.ValueOf(models)

	setFunc := reflect.MakeFunc(setFuncType, func(args []reflect.Value) []reflect.Value {
		builder := args[0]
		index := int(args[1].Int())
		model := modelsVal.Index(index).Elem()
		modelVal := model.Elem()

		r.setFields(builder, modelVal)
		return nil
	})

	bulkBuilder := mapCreateBulkMethod.Call([]reflect.Value{
		modelsVal,
		setFunc,
	})[0]

	saveMethod := bulkBuilder.MethodByName(MethodSave)
	results := saveMethod.Call([]reflect.Value{reflect.ValueOf(ctx)})
	if errVal := results[1]; !errVal.IsNil() {
		return errVal.Interface().(error)
	}

	savedEntities := results[0]
	for i := 0; i < savedEntities.Len(); i++ {
		savedModel := savedEntities.Index(i).Elem()
		modelsVal.Index(i).Elem().Elem().Set(savedModel)
	}

	return nil
}

// BatchDelete removes multiple models by ID.
func (r *BaseRepository[T, PT, ID]) BatchDelete(ctx context.Context, ids []ID) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			if IsNotFound(err) {
				continue
			}
			return err
		}
	}
	return nil
}
