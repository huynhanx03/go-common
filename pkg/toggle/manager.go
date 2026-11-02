package toggle

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

// Manager validates service-owned definitions and delegates atomic persistence
// to a Store. It does not cache snapshots or evaluate flags.
type Manager struct {
	store Store
}

// NewManager creates a manager backed by a caller-owned Store.
func NewManager(store Store) (*Manager, error) {
	if core.IsNil(store) {
		return nil, ErrInvalidConfig
	}
	return &Manager{store: store}, nil
}

func (m *Manager) valid() bool { return m != nil && !core.IsNil(m.store) }

func validateScopeContext(ctx context.Context, scope Scope) error {
	if err := core.ValidateContext(ctx); err != nil {
		return err
	}
	return ValidateScope(scope)
}

func (m *Manager) validateStore(ctx context.Context, scope Scope) error {
	if !m.valid() {
		return ErrInvalidConfig
	}
	return validateScopeContext(ctx, scope)
}

func (m *Manager) loadSnapshot(ctx context.Context, scope Scope) (Snapshot, error) {
	snapshot, err := m.store.Load(ctx, scope)
	if err != nil {
		return Snapshot{}, core.SourceError(err)
	}
	normalized, err := NormalizeSnapshot(scope, snapshot)
	if err != nil {
		return Snapshot{}, core.SourceError(err)
	}
	return normalized, nil
}

// List returns a sorted, owned snapshot after validating the complete source
// response. A malformed response never leaks a partial result.
func (m *Manager) List(ctx context.Context, scope Scope) (Snapshot, error) {
	if err := m.validateStore(ctx, scope); err != nil {
		return Snapshot{}, err
	}
	return m.loadSnapshot(ctx, scope)
}

// Get returns an owned flag and the revision observed in the same snapshot.
// A missing key retains the successfully read revision for a safe retry.
func (m *Manager) Get(ctx context.Context, scope Scope, key string) (Flag, int64, error) {
	if err := validateScopeContext(ctx, scope); err != nil {
		return Flag{}, 0, err
	}
	if err := core.Identifier(key, core.MaxIdentifierLength, ErrInvalidDefinition); err != nil {
		return Flag{}, 0, err
	}
	if !m.valid() {
		return Flag{}, 0, ErrInvalidConfig
	}
	snapshot, err := m.loadSnapshot(ctx, scope)
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
	if err := m.validateStore(ctx, scope); err != nil {
		return 0, err
	}
	if err := core.ValidateExpectedRevision(expectedRevision); err != nil {
		return 0, err
	}
	normalized, err := NormalizeFlag(flag)
	if err != nil {
		return 0, err
	}
	revision, err := m.store.Put(ctx, scope, normalized, expectedRevision)
	if err != nil {
		return 0, operationError{operation: "write failed", cause: err}
	}
	return revision, nil
}

// Delete removes one definition with a scope-level revision precondition.
func (m *Manager) Delete(ctx context.Context, scope Scope, key string, expectedRevision int64) (int64, error) {
	if err := m.validateStore(ctx, scope); err != nil {
		return 0, err
	}
	if err := core.Identifier(key, core.MaxIdentifierLength, ErrInvalidDefinition); err != nil {
		return 0, err
	}
	if err := core.ValidateExpectedRevision(expectedRevision); err != nil {
		return 0, err
	}
	revision, err := m.store.Delete(ctx, scope, key, expectedRevision)
	if err != nil {
		return 0, operationError{operation: "delete failed", cause: err}
	}
	return revision, nil
}
