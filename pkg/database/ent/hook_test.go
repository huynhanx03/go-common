package ent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	entgo "entgo.io/ent"
)

type hookMutation struct {
	entgo.Mutation
	op      entgo.Op
	fields  map[string]entgo.Value
	added   map[string]entgo.Value
	cleared map[string]bool
}

func (m hookMutation) Op() entgo.Op { return m.op }

func (m hookMutation) Field(name string) (entgo.Value, bool) {
	value, ok := m.fields[name]
	return value, ok
}

func (m hookMutation) AddedField(name string) (entgo.Value, bool) {
	value, ok := m.added[name]
	return value, ok
}

func (m hookMutation) FieldCleared(name string) bool { return m.cleared[name] }

func TestMutationConditionsComposeAndShortCircuit(t *testing.T) {
	mutation := hookMutation{
		op:      entgo.OpUpdateOne,
		fields:  map[string]entgo.Value{"name": "Ada"},
		added:   map[string]entgo.Value{"visits": 1},
		cleared: map[string]bool{"nickname": true},
	}
	ctx := context.Background()

	if !And(HasOp(entgo.OpUpdateOne), HasFields("name"), HasAddedFields("visits"), HasClearedFields("nickname"))(ctx, mutation) {
		t.Fatal("AND condition did not accept an update with all required changes")
	}
	if !Or(HasOp(entgo.OpCreate), HasOp(entgo.OpUpdateOne))(ctx, mutation) {
		t.Fatal("OR condition did not accept a matching operation")
	}
	if Not(HasOp(entgo.OpCreate))(ctx, mutation) != true {
		t.Fatal("NOT condition did not invert the operation check")
	}

	called := false
	if And(
		func(context.Context, entgo.Mutation) bool { return false },
		func(context.Context, entgo.Mutation) bool { called = true; return true },
	)(ctx, mutation) || called {
		t.Fatal("AND evaluated later conditions after the first failure")
	}
	called = false
	if !Or(
		func(context.Context, entgo.Mutation) bool { return true },
		func(context.Context, entgo.Mutation) bool { called = true; return false },
	)(ctx, mutation) || called {
		t.Fatal("OR evaluated later conditions after the first match")
	}
}

func TestFieldConditionsRequireEveryRequestedField(t *testing.T) {
	mutation := hookMutation{
		fields:  map[string]entgo.Value{"name": "Ada"},
		added:   map[string]entgo.Value{"visits": 1},
		cleared: map[string]bool{"nickname": true},
	}
	ctx := context.Background()
	for _, condition := range []Condition{
		HasFields("name"),
		HasAddedFields("visits"),
		HasClearedFields("nickname"),
	} {
		if !condition(ctx, mutation) {
			t.Fatal("condition rejected a field present in the mutation")
		}
	}
	for _, condition := range []Condition{
		HasFields("name", "missing"),
		HasAddedFields("visits", "missing"),
		HasClearedFields("nickname", "missing"),
		HasClearedFields("not_cleared"),
	} {
		if condition(ctx, mutation) {
			t.Fatal("condition accepted a mutation missing a required field change")
		}
	}
}

func TestConditionalHooksGateMutations(t *testing.T) {
	ctx := context.Background()
	mutations := []hookMutation{{op: entgo.OpCreate}, {op: entgo.OpUpdateOne}}
	baseCalls := 0
	base := entgo.MutateFunc(func(context.Context, entgo.Mutation) (entgo.Value, error) {
		baseCalls++
		return "saved", nil
	})
	hookCalls := 0
	marked := entgo.Hook(func(next entgo.Mutator) entgo.Mutator {
		return entgo.MutateFunc(func(ctx context.Context, mutation entgo.Mutation) (entgo.Value, error) {
			hookCalls++
			return next.Mutate(ctx, mutation)
		})
	})

	createOnly := On(marked, entgo.OpCreate)
	for _, mutation := range mutations {
		if _, err := createOnly(base).Mutate(ctx, mutation); err != nil {
			t.Fatal(err)
		}
	}
	if hookCalls != 1 || baseCalls != 2 {
		t.Fatalf("On hook calls=%d, base calls=%d; want 1 and 2", hookCalls, baseCalls)
	}

	hookCalls = 0
	updateExcluded := Unless(marked, entgo.OpUpdateOne)
	for _, mutation := range mutations {
		if _, err := updateExcluded(base).Mutate(ctx, mutation); err != nil {
			t.Fatal(err)
		}
	}
	if hookCalls != 1 {
		t.Fatalf("Unless hook calls=%d; want create only", hookCalls)
	}

	conditioned := If(marked, HasOp(entgo.OpUpdateOne))
	hookCalls = 0
	for _, mutation := range mutations {
		if _, err := conditioned(base).Mutate(ctx, mutation); err != nil {
			t.Fatal(err)
		}
	}
	if hookCalls != 1 {
		t.Fatalf("If hook calls=%d; want update only", hookCalls)
	}

	denied := Reject(entgo.OpDelete)
	_, err := denied(base).Mutate(ctx, hookMutation{op: entgo.OpDelete})
	if err == nil {
		t.Fatal("Reject allowed a forbidden delete")
	}
	if baseCalls != 6 {
		t.Fatalf("base mutator calls=%d; rejected operation must not reach it", baseCalls)
	}

	sentinel := errors.New("blocked by policy")
	_, err = FixedError(sentinel)(base).Mutate(ctx, mutations[0])
	if !errors.Is(err, sentinel) {
		t.Fatalf("FixedError returned %v; want wrapped policy failure", err)
	}
}

func TestChainPreservesExecutionOrderAndAppendReturnsNewChain(t *testing.T) {
	var calls []string
	wrap := func(name string) entgo.Hook {
		return func(next entgo.Mutator) entgo.Mutator {
			return entgo.MutateFunc(func(ctx context.Context, mutation entgo.Mutation) (entgo.Value, error) {
				calls = append(calls, "before:"+name)
				value, err := next.Mutate(ctx, mutation)
				calls = append(calls, "after:"+name)
				return value, err
			})
		}
	}

	initial := []entgo.Hook{wrap("first")}
	chain := NewChain(initial...)
	initial[0] = wrap("replaced")
	combined := chain.Append(wrap("second")).Extend(NewChain(wrap("third")))
	next := entgo.MutateFunc(func(context.Context, entgo.Mutation) (entgo.Value, error) {
		calls = append(calls, "mutation")
		return "saved", nil
	})
	value, err := combined.Hook()(next).Mutate(context.Background(), hookMutation{})
	if err != nil || value != "saved" {
		t.Fatalf("chained mutation = %v, %v", value, err)
	}
	want := []string{"before:first", "before:second", "before:third", "mutation", "after:third", "after:second", "after:first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("hook order = %v; want %v", calls, want)
	}

	calls = nil
	if _, err := chain.Hook()(next).Mutate(context.Background(), hookMutation{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"before:first", "mutation", "after:first"}) {
		t.Fatalf("appending changed the original chain: %v", calls)
	}
}
