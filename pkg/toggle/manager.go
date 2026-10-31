package toggle

import (
	"context"
	"math"
	"reflect"
)

// Manager validates service-owned definitions and delegates atomic persistence
// to a Store. It does not cache snapshots or evaluate flags.
type Manager struct {
	store Store
}

// NewManager creates a manager backed by a caller-owned Store.
func NewManager(store Store) (*Manager, error) {
	if nilStore(store) {
		return nil, ErrInvalidConfig
	}
	return &Manager{store: store}, nil
}

func nilStore(store Store) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func managerContextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return categorizedError{category: ErrInvalidContext, cause: err}
	}
	return nil
}

func (m *Manager) valid() bool { return m != nil && !nilStore(m.store) }

// List returns a sorted, owned snapshot after validating the complete source
// response. A malformed response never leaks a partial result.
func (m *Manager) List(ctx context.Context, scope Scope) (Snapshot, error) {
	if !m.valid() {
		return Snapshot{}, ErrInvalidConfig
	}
	if err := managerContextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := ValidateScope(scope); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := m.store.Load(ctx, scope)
	if err != nil {
		return Snapshot{}, categorizedError{category: ErrSource, cause: err}
	}
	normalized, err := NormalizeSnapshot(scope, snapshot)
	if err != nil {
		return Snapshot{}, categorizedError{category: ErrSource, cause: err}
	}
	return normalized, nil
}

// Get returns an owned flag and the revision observed in the same snapshot.
// A missing key retains the successfully read revision for a safe retry.
func (m *Manager) Get(ctx context.Context, scope Scope, key string) (Flag, int64, error) {
	if err := managerContextError(ctx); err != nil {
		return Flag{}, 0, err
	}
	if err := ValidateScope(scope); err != nil {
		return Flag{}, 0, err
	}
	if err := identifier(key, maxIdentifier, ErrInvalidDefinition); err != nil {
		return Flag{}, 0, err
	}
	snapshot, err := m.List(ctx, scope)
	if err != nil {
		return Flag{}, 0, err
	}
	for _, flag := range snapshot.Flags {
		if flag.Key == key {
			return flag, snapshot.Revision, nil
		}
	}
	return Flag{}, snapshot.Revision, ErrNotFound
}

// Put writes a complete normalized definition using the caller's revision
// precondition. The Store owns conflict detection and transaction semantics.
func (m *Manager) Put(ctx context.Context, scope Scope, flag Flag, expectedRevision int64) (int64, error) {
	if !m.valid() {
		return 0, ErrInvalidConfig
	}
	if err := managerContextError(ctx); err != nil {
		return 0, err
	}
	if err := ValidateScope(scope); err != nil {
		return 0, err
	}
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return 0, ErrInvalidDefinition
	}
	normalized, err := NormalizeFlag(flag)
	if err != nil {
		return 0, err
	}
	revision, err := m.store.Put(ctx, scope, normalized, expectedRevision)
	if err != nil {
		return 0, operationError{operation: "toggle: write failed", cause: err}
	}
	return revision, nil
}

// Delete removes one definition with a scope-level revision precondition.
func (m *Manager) Delete(ctx context.Context, scope Scope, key string, expectedRevision int64) (int64, error) {
	if !m.valid() {
		return 0, ErrInvalidConfig
	}
	if err := managerContextError(ctx); err != nil {
		return 0, err
	}
	if err := ValidateScope(scope); err != nil {
		return 0, err
	}
	if err := identifier(key, maxIdentifier, ErrInvalidDefinition); err != nil {
		return 0, err
	}
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return 0, ErrInvalidDefinition
	}
	revision, err := m.store.Delete(ctx, scope, key, expectedRevision)
	if err != nil {
		return 0, operationError{operation: "toggle: delete failed", cause: err}
	}
	return revision, nil
}
