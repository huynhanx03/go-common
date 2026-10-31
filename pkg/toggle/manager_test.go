package toggle_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/toggle"
)

func TestManagerCompleteDefinitionLifecycle(t *testing.T) {
	store := &recordingStore{
		snapshot: toggle.Snapshot{Scope: managerScope, Revision: 3, Flags: []toggle.Flag{
			{Key: "z-last", Enabled: false},
			{Key: "checkout", Enabled: true},
		}},
		putRevision: 4,
		delRevision: 5,
	}
	manager, err := toggle.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := manager.List(context.Background(), managerScope)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Revision != 3 || len(listed.Flags) != 2 || listed.Flags[0].Key != "checkout" || listed.Flags[1].Key != "z-last" {
		t.Fatalf("list=%+v", listed)
	}
	got, revision, err := manager.Get(context.Background(), managerScope, "checkout")
	if err != nil || revision != 3 || got.Key != "checkout" {
		t.Fatalf("get=%+v revision=%d err=%v", got, revision, err)
	}

	input := toggle.Flag{
		Key: "checkout", Description: "new checkout", Enabled: true,
		Rules: []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{
			ContextName: "plan", Operator: "IN", Values: []string{"pro"},
		}}, Variants: []toggle.Variant{{Name: "v2", Weight: 1000, Payload: &toggle.Payload{Type: "json", Value: `{"version":2}`}}}}},
	}
	revision, err = manager.Put(context.Background(), managerScope, input, 3)
	if err != nil || revision != 4 {
		t.Fatalf("put revision=%d err=%v", revision, err)
	}
	if store.puts != 1 || store.putExpected != 3 || store.lastScope != managerScope {
		t.Fatalf("store put calls=%d expected=%d scope=%+v", store.puts, store.putExpected, store.lastScope)
	}
	saved := store.lastFlag
	if saved.Rules[0].GroupID != "checkout" || saved.Rules[0].Stickiness != "userId" {
		t.Fatalf("rule defaults not persisted: %+v", saved.Rules[0])
	}
	if saved.Rules[0].Constraints[0].Values[0] != "pro" || saved.Rules[0].Variants[0].Payload.Value != `{"version":2}` {
		t.Fatalf("put did not retain complete definition: %+v", saved)
	}
	input.Description = "mutated"
	input.Rules[0].Constraints[0].Values[0] = "mutated"
	input.Rules[0].Variants[0].Payload.Value = "mutated"
	if store.lastFlag.Description != "new checkout" || store.lastFlag.Rules[0].Constraints[0].Values[0] != "pro" || store.lastFlag.Rules[0].Variants[0].Payload.Value != `{"version":2}` {
		t.Fatalf("store received caller-owned memory: %+v", store.lastFlag)
	}

	revision, err = manager.Delete(context.Background(), managerScope, "checkout", 4)
	if err != nil || revision != 5 {
		t.Fatalf("delete revision=%d err=%v", revision, err)
	}
	if store.deletes != 1 || store.deleteExpected != 4 || store.lastKey != "checkout" {
		t.Fatalf("store delete calls=%d expected=%d key=%q", store.deletes, store.deleteExpected, store.lastKey)
	}
}

func TestManagerRejectsMissingStore(t *testing.T) {
	if _, err := toggle.NewManager(nil); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("NewManager(nil) error=%v", err)
	}
}

func TestManagerRejectsInvalidWritesBeforeStore(t *testing.T) {
	valid := func() toggle.Flag { return toggle.Flag{Key: "checkout", Enabled: true} }
	makeVariants := func(first, second int) []toggle.Variant {
		return []toggle.Variant{{Name: "one", Weight: first}, {Name: "two", Weight: second}}
	}
	oversizeDefinition := make([]string, 65)
	for i := range oversizeDefinition {
		oversizeDefinition[i] = strings.Repeat("x", 1024)
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name  string
		ctx   context.Context
		scope toggle.Scope
		flag  toggle.Flag
		rev   int64
		want  []error
	}{
		{name: "rollout below range", scope: managerScope, flag: func() toggle.Flag { f := valid(); f.Rules = []toggle.Rule{{Rollout: -1}}; return f }(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "rollout above range", scope: managerScope, flag: func() toggle.Flag { f := valid(); f.Rules = []toggle.Rule{{Rollout: 101}}; return f }(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "variant weights below total", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Variants: makeVariants(500, 499)}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "variant weights above total", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Variants: makeVariants(500, 501)}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "duplicate variant names", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Variants: []toggle.Variant{{Name: "same", Weight: 500}, {Name: "same", Weight: 500}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "invalid JSON payload", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Variants: []toggle.Variant{{Name: "v1", Weight: 1000, Payload: &toggle.Payload{Type: "json", Value: "{"}}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "unsupported operator", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "plan", Operator: "REGEX", Value: "pro"}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "empty list operands", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "plan", Operator: "IN"}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "empty numeric operand", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "score", Operator: "NUM_EQ"}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "NaN numeric operand", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "score", Operator: "NUM_EQ", Value: "NaN"}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "infinite numeric operand", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "score", Operator: "NUM_GT", Value: "Inf"}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "malformed flag identifier", scope: managerScope, flag: func() toggle.Flag { f := valid(); f.Key = "bad key"; return f }(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "oversize flag identifier", scope: managerScope, flag: func() toggle.Flag { f := valid(); f.Key = strings.Repeat("a", 101); return f }(), want: []error{toggle.ErrInvalidDefinition, toggle.ErrLimit}},
		{name: "malformed constraint property", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "bad name", Operator: "IN", Values: []string{"x"}}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition}},
		{name: "too many rules", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = make([]toggle.Rule, 33)
			for i := range f.Rules {
				f.Rules[i].Rollout = 100
			}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition, toggle.ErrLimit}},
		{name: "too many values", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			values := make([]string, 1001)
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "plan", Operator: "IN", Values: values}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition, toggle.ErrLimit}},
		{name: "encoded definition over limit", scope: managerScope, flag: func() toggle.Flag {
			f := valid()
			f.Rules = []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "plan", Operator: "IN", Values: oversizeDefinition}}}}
			return f
		}(), want: []error{toggle.ErrInvalidDefinition, toggle.ErrLimit}},
		{name: "invalid scope", scope: toggle.Scope{Namespace: "", Environment: "test"}, flag: valid(), want: []error{toggle.ErrInvalidConfig}},
		{name: "negative revision", scope: managerScope, flag: valid(), rev: -1, want: []error{toggle.ErrInvalidDefinition}},
		{name: "nil context", ctx: nil, scope: managerScope, flag: valid(), want: []error{toggle.ErrInvalidContext}},
		{name: "canceled context", ctx: canceledCtx, scope: managerScope, flag: valid(), want: []error{context.Canceled}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := &recordingStore{putRevision: 1}
			manager, err := toggle.NewManager(store)
			if err != nil {
				t.Fatal(err)
			}
			ctx := test.ctx
			if ctx == nil && test.name != "nil context" {
				ctx = context.Background()
			}
			_, err = manager.Put(ctx, test.scope, test.flag, test.rev)
			if err == nil {
				t.Fatal("Put succeeded with invalid input")
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Errorf("Put error %v does not match %v", err, want)
				}
			}
			if store.puts != 0 || store.deletes != 0 {
				t.Fatalf("invalid write reached Store: puts=%d deletes=%d", store.puts, store.deletes)
			}
		})
	}
}

func TestManagerReadIsolationAndErrors(t *testing.T) {
	borrowed := toggle.Flag{Key: "checkout", Enabled: true, Rules: []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{{ContextName: "plan", Operator: "IN", Values: []string{"pro"}}}, Variants: []toggle.Variant{{Name: "v1", Weight: 1000, Payload: &toggle.Payload{Type: "string", Value: "stable"}}}}}}
	store := &recordingStore{snapshot: toggle.Snapshot{Scope: managerScope, Revision: 7, Flags: []toggle.Flag{borrowed}}}
	manager, err := toggle.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := manager.List(context.Background(), managerScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Flags) != 1 {
		t.Fatalf("List returned %d flags, want 1", len(listed.Flags))
	}
	listed.Flags[0].Rules[0].Constraints[0].Values[0] = "mutated"
	listed.Flags[0].Rules[0].Variants[0].Payload.Value = "mutated"
	got, revision, err := manager.Get(context.Background(), managerScope, "checkout")
	if err != nil || revision != 7 || got.Rules[0].Constraints[0].Values[0] != "pro" || got.Rules[0].Variants[0].Payload.Value != "stable" {
		t.Fatalf("manager returned borrowed data: flag=%+v revision=%d err=%v", got, revision, err)
	}
	got.Rules[0].Constraints[0].Values[0] = "changed again"
	got.Rules[0].Variants[0].Payload.Value = "changed again"
	got, _, err = manager.Get(context.Background(), managerScope, "checkout")
	if err != nil || got.Rules[0].Constraints[0].Values[0] != "pro" || got.Rules[0].Variants[0].Payload.Value != "stable" {
		t.Fatalf("Get returned borrowed data: flag=%+v err=%v", got, err)
	}

	missing, current, err := manager.Get(context.Background(), managerScope, "missing")
	if !errors.Is(err, toggle.ErrNotFound) || current != 7 || missing.Key != "" {
		t.Fatalf("missing get=%+v revision=%d err=%v", missing, current, err)
	}

	invalid := []struct {
		name     string
		snapshot toggle.Snapshot
	}{
		{name: "wrong scope", snapshot: toggle.Snapshot{Scope: toggle.Scope{Namespace: "other", Environment: managerScope.Environment}, Revision: 1}},
		{name: "revision zero with flags", snapshot: toggle.Snapshot{Scope: managerScope, Revision: 0, Flags: []toggle.Flag{{Key: "checkout"}}}},
		{name: "duplicate keys", snapshot: toggle.Snapshot{Scope: managerScope, Revision: 1, Flags: []toggle.Flag{{Key: "checkout"}, {Key: "checkout"}}}},
		{name: "negative revision", snapshot: toggle.Snapshot{Scope: managerScope, Revision: -1}},
	}
	for _, candidate := range invalid {
		t.Run(candidate.name, func(t *testing.T) {
			badStore := &recordingStore{snapshot: candidate.snapshot}
			badManager, err := toggle.NewManager(badStore)
			if err != nil {
				t.Fatal(err)
			}
			got, err := badManager.List(context.Background(), managerScope)
			if !errors.Is(err, toggle.ErrSource) || !errors.Is(err, toggle.ErrInvalidDefinition) || len(got.Flags) != 0 {
				t.Fatalf("malformed source snapshot=%+v err=%v", got, err)
			}
		})
	}

	storageErr := errors.New("database unavailable")
	badStore := &recordingStore{loadErr: storageErr}
	badManager, _ := toggle.NewManager(badStore)
	if got, err := badManager.List(context.Background(), managerScope); !errors.Is(err, toggle.ErrSource) || !errors.Is(err, storageErr) || got.Revision != 0 || len(got.Flags) != 0 {
		t.Fatalf("source error snapshot=%+v err=%v", got, err)
	}
	if flag, revision, err := badManager.Get(context.Background(), managerScope, "checkout"); !errors.Is(err, toggle.ErrSource) || !errors.Is(err, storageErr) || revision != 0 || flag.Key != "" {
		t.Fatalf("failed get flag=%+v revision=%d err=%v", flag, revision, err)
	}

	conflictStore := &recordingStore{putErr: toggle.ErrConflict, putRevision: 99, delErr: toggle.ErrConflict, delRevision: 99}
	conflictManager, _ := toggle.NewManager(conflictStore)
	if revision, err := conflictManager.Put(context.Background(), managerScope, validManagerFlag("checkout"), 1); revision != 0 || !errors.Is(err, toggle.ErrConflict) {
		t.Fatalf("failed Put revision=%d err=%v", revision, err)
	}
	if revision, err := conflictManager.Delete(context.Background(), managerScope, "checkout", 1); revision != 0 || !errors.Is(err, toggle.ErrConflict) {
		t.Fatalf("failed Delete revision=%d err=%v", revision, err)
	}
	writeCause := errors.New("sensitive database detail")
	writeStore := &recordingStore{putErr: writeCause, putRevision: 99}
	writeManager, _ := toggle.NewManager(writeStore)
	if revision, err := writeManager.Put(context.Background(), managerScope, validManagerFlag("checkout"), 1); revision != 0 || !errors.Is(err, writeCause) {
		t.Fatalf("write cause was not preserved: revision=%d err=%v", revision, err)
	} else if strings.Contains(err.Error(), writeCause.Error()) {
		t.Fatalf("outer write error exposed storage details: %v", err)
	}
}

func TestManagerPreservesRuleOrderAndExactByteBoundaries(t *testing.T) {
	boundaryScope := toggle.Scope{Namespace: strings.Repeat("n", 100), Environment: strings.Repeat("e", 100)}
	wideName := strings.Repeat("p", 1024)
	wideValue := strings.Repeat("é", 512)
	boundaryFlag := toggle.Flag{
		Key: strings.Repeat("k", 100), Description: strings.Repeat("d", 4096), Enabled: true,
		Rules: []toggle.Rule{
			{Rollout: 100, GroupID: strings.Repeat("g", 100), Stickiness: wideName, Constraints: []toggle.Constraint{{ContextName: wideName, Operator: "IN", Values: []string{wideValue}}}, Variants: []toggle.Variant{{Name: "v1", Weight: 1000, Payload: &toggle.Payload{Type: "string", Value: wideValue}}}},
			{Rollout: 25, GroupID: "second"},
		},
	}
	store := &recordingStore{putRevision: 1, snapshot: toggle.Snapshot{Scope: boundaryScope, Revision: 1, Flags: []toggle.Flag{{Key: "z-last"}, boundaryFlag}}}
	manager, err := toggle.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	if revision, err := manager.Put(context.Background(), boundaryScope, boundaryFlag, 0); err != nil || revision != 1 {
		t.Fatalf("exact-byte-limit Put revision=%d err=%v", revision, err)
	}
	saved := store.lastFlag
	if saved.Key != strings.Repeat("k", 100) || saved.Description != strings.Repeat("d", 4096) || saved.Rules[0].Stickiness != wideName || saved.Rules[0].Constraints[0].Values[0] != wideValue {
		t.Fatalf("valid boundary definition changed: %+v", saved)
	}
	listed, err := manager.List(context.Background(), boundaryScope)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Flags[0].Key != strings.Repeat("k", 100) || listed.Flags[1].Key != "z-last" {
		t.Fatalf("flags not sorted: %+v", listed.Flags)
	}
	if len(listed.Flags[0].Rules) != 2 || listed.Flags[0].Rules[0].GroupID != strings.Repeat("g", 100) || listed.Flags[0].Rules[1].GroupID != "second" {
		t.Fatalf("rule order changed: %+v", listed.Flags[0].Rules)
	}
	if len(listed.Flags[0].Rules[0].Constraints[0].Values[0]) != 1024 || len(listed.Flags[0].Rules[0].Variants[0].Payload.Value) != 1024 {
		t.Fatal("multibyte operands were not measured in bytes")
	}
}
