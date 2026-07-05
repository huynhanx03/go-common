package ent

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
)

// fakeMutation is a minimal ent.Mutation for hook tests. Unused interface
// methods come from the nil embedded interface (panic if hit).
type fakeMutation struct {
	ent.Mutation
	op     ent.Op
	fields map[string]ent.Value

	createdBy, updatedBy, deletedBy string
	deletedAt                       *time.Time
	setOp                           ent.Op
	predicates                      int
}

func (m *fakeMutation) Op() ent.Op { return m.op }
func (m *fakeMutation) Field(name string) (ent.Value, bool) {
	v, ok := m.fields[name]
	return v, ok
}
func (m *fakeMutation) SetCreatedBy(s string)    { m.createdBy = s }
func (m *fakeMutation) SetUpdatedBy(s string)    { m.updatedBy = s }
func (m *fakeMutation) SetDeletedBy(s string)    { m.deletedBy = s }
func (m *fakeMutation) SetDeletedAt(t time.Time) { m.deletedAt = &t }
func (m *fakeMutation) SetOp(op ent.Op)          { m.setOp = op }
func (m *fakeMutation) WhereP(ps ...func(*sql.Selector)) {
	m.predicates += len(ps)
}

func withActor(t *testing.T, actor string) {
	t.Helper()
	SetActorResolver(func(context.Context) (string, bool) { return actor, actor != "" })
	t.Cleanup(func() { SetActorResolver(nil) })
}

// nextRecorder is a terminal mutator recording whether it ran.
type nextRecorder struct{ called bool }

func (n *nextRecorder) Mutate(context.Context, ent.Mutation) (ent.Value, error) {
	n.called = true
	return nil, nil
}

func TestModifierHookStampsActor(t *testing.T) {
	withActor(t, "user-1")
	hook := ModifierMixin{}.Hooks()[0]
	ctx := context.Background()

	create := &fakeMutation{op: ent.OpCreate, fields: map[string]ent.Value{}}
	if _, err := hook(&nextRecorder{}).Mutate(ctx, create); err != nil {
		t.Fatal(err)
	}
	if create.createdBy != "user-1" {
		t.Fatalf("created_by = %q, want user-1", create.createdBy)
	}

	update := &fakeMutation{op: ent.OpUpdateOne, fields: map[string]ent.Value{}}
	if _, err := hook(&nextRecorder{}).Mutate(ctx, update); err != nil {
		t.Fatal(err)
	}
	if update.updatedBy != "user-1" {
		t.Fatalf("updated_by = %q, want user-1", update.updatedBy)
	}
}

func TestModifierHookRespectsExplicitValue(t *testing.T) {
	withActor(t, "user-1")
	hook := ModifierMixin{}.Hooks()[0]

	m := &fakeMutation{
		op:     ent.OpCreate,
		fields: map[string]ent.Value{CreatedByColumnName: "importer"},
	}
	if _, err := hook(&nextRecorder{}).Mutate(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if m.createdBy != "" {
		t.Fatalf("explicitly set created_by was overridden with %q", m.createdBy)
	}
}

func TestModifierHookSkip(t *testing.T) {
	withActor(t, "user-1")
	hook := ModifierMixin{}.Hooks()[0]

	m := &fakeMutation{op: ent.OpCreate, fields: map[string]ent.Value{}}
	if _, err := hook(&nextRecorder{}).Mutate(SkipModifier(context.Background()), m); err != nil {
		t.Fatal(err)
	}
	if m.createdBy != "" {
		t.Fatalf("created_by = %q, want empty under SkipModifier", m.createdBy)
	}
}

func TestModifierHookNoResolver(t *testing.T) {
	SetActorResolver(nil)
	hook := ModifierMixin{}.Hooks()[0]

	m := &fakeMutation{op: ent.OpCreate, fields: map[string]ent.Value{}}
	if _, err := hook(&nextRecorder{}).Mutate(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if m.createdBy != "" {
		t.Fatalf("created_by = %q, want empty without resolver", m.createdBy)
	}
}

func TestSoftDeleteHookConvertsToUpdate(t *testing.T) {
	withActor(t, "user-1")

	bridged := false
	hook := SoftDeleteHook(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
		bridged = true
		return nil, nil
	})

	m := &fakeMutation{op: ent.OpDeleteOne, fields: map[string]ent.Value{}}
	if _, err := hook(&nextRecorder{}).Mutate(context.Background(), m); err != nil {
		t.Fatal(err)
	}

	if !bridged {
		t.Fatal("bridge was not invoked")
	}
	if m.setOp != ent.OpUpdate {
		t.Fatalf("op = %v, want OpUpdate", m.setOp)
	}
	if m.deletedAt == nil {
		t.Fatal("deleted_at was not set")
	}
	if m.deletedBy != "user-1" {
		t.Fatalf("deleted_by = %q, want user-1", m.deletedBy)
	}
	if m.predicates == 0 {
		t.Fatal("deleted_at IS NULL predicate was not added")
	}
}

func TestSoftDeleteHookSkipDeletesPermanently(t *testing.T) {
	hook := SoftDeleteHook(func(context.Context, ent.Mutation) (ent.Value, error) {
		t.Fatal("bridge must not run under SkipSoftDelete")
		return nil, nil
	})

	next := &nextRecorder{}
	m := &fakeMutation{op: ent.OpDelete, fields: map[string]ent.Value{}}
	if _, err := hook(next).Mutate(SkipSoftDelete(context.Background()), m); err != nil {
		t.Fatal(err)
	}
	if !next.called {
		t.Fatal("hard delete must reach the next mutator")
	}
	if m.deletedAt != nil {
		t.Fatal("deleted_at must stay empty on hard delete")
	}
}

func TestSoftDeleteInterceptorFiltersQueries(t *testing.T) {
	interceptor := SoftDeleteMixin{}.Interceptors()[0].(ent.TraverseFunc)

	q := &fakeMutation{} // has WhereP, that's all Traverse needs
	if err := interceptor.Traverse(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if q.predicates != 1 {
		t.Fatalf("predicates = %d, want 1", q.predicates)
	}

	skipped := &fakeMutation{}
	if err := interceptor.Traverse(SkipSoftDelete(context.Background()), skipped); err != nil {
		t.Fatal(err)
	}
	if skipped.predicates != 0 {
		t.Fatalf("predicates = %d, want 0 under SkipSoftDelete", skipped.predicates)
	}
}

func TestPublicIDMixinDefault(t *testing.T) {
	fields := PublicIDMixin{Prefix: "UR"}.Fields()
	gen, ok := fields[0].Descriptor().Default.(func() string)
	if !ok {
		t.Fatalf("default is %T, want func() string", fields[0].Descriptor().Default)
	}
	id := gen()
	if len(id) != len("UR")+8+3 || id[:2] != "UR" {
		t.Fatalf("unexpected public id %q", id)
	}
}

func TestNewUUIDIsTimeOrdered(t *testing.T) {
	a, b := NewUUID(), NewUUID()
	if a == b {
		t.Fatal("UUIDs must be unique")
	}
	if a.Version() != 7 {
		t.Fatalf("version = %d, want 7", a.Version())
	}
	if a.String() > b.String() {
		t.Fatalf("v7 UUIDs must sort chronologically: %s > %s", a, b)
	}
}
