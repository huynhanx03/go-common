package core

import (
	"context"
	"reflect"
)

// Client evaluates flags locally within its bound scope.
type Client interface {
	IsEnabled(context.Context, string, EvaluationContext, bool) (bool, error)
	GetVariant(context.Context, string, EvaluationContext) (VariantResult, error)
	Close(context.Context) error
}

// Source loads one complete consistent snapshot. Implementations must honor ctx.
type Source interface {
	Load(context.Context, Scope) (Snapshot, error)
}

// Store persists complete definitions using scope-level revision preconditions.
// Implementations must validate direct calls and return owned snapshots.
type Store interface {
	Source
	Put(context.Context, Scope, Flag, int64) (int64, error)
	Delete(context.Context, Scope, string, int64) (int64, error)
}

// ValidateContext rejects missing or canceled operation contexts.
func ValidateContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return Wrap(ErrInvalidContext, err)
	}
	return nil
}

// IsNil reports whether value is nil, including a typed nil held by an interface.
func IsNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
