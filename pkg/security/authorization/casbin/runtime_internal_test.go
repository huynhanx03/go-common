package casbin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

const internalRBACModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`

func TestOptionsAndLimits(t *testing.T) {
	t.Parallel()

	if _, err := NewRuntime(internalSnapshot(1), nil); !errors.Is(
		err,
		ErrInvalidOption,
	) {
		t.Fatalf("NewRuntime(nil option) error = %v", err)
	}
	if _, err := NewRuntime(
		internalSnapshot(1),
		WithClock(nil),
	); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("WithClock(nil) error = %v", err)
	}

	validLimits := Limits{
		MaxModelBytes: len(internalRBACModel) + 1,
		MaxRules:      2,
		MaxValueBytes: 32,
	}
	if _, err := NewRuntime(
		internalSnapshot(1),
		WithLimits(validLimits),
	); err != nil {
		t.Fatalf("WithLimits(valid) error = %v", err)
	}
	for _, limits := range []Limits{
		{},
		{MaxModelBytes: maxModelBytes + 1, MaxRules: 1, MaxValueBytes: 1},
		{MaxModelBytes: 1, MaxRules: maxRules + 1, MaxValueBytes: 1},
		{MaxModelBytes: 1, MaxRules: 1, MaxValueBytes: maxValueBytes + 1},
	} {
		if _, err := NewRuntime(
			internalSnapshot(1),
			WithLimits(limits),
		); !errors.Is(err, ErrInvalidOption) {
			t.Errorf("WithLimits(%#v) error = %v", limits, err)
		}
	}
}

func TestDefaultRoleHierarchyDepthBoundary(t *testing.T) {
	t.Parallel()

	snapshot := Snapshot{Revision: 1, Model: internalRBACModel}
	snapshot.Rules = append(snapshot.Rules,
		PolicyRule{Type: "p", V0: "role-9", V1: "document", V2: "within"},
		PolicyRule{Type: "p", V0: "role-10", V1: "document", V2: "beyond"},
		PolicyRule{Type: "g", V0: "user", V1: "role-0"},
	)
	for depth := 0; depth < DefaultMaxRoleHierarchyDepth; depth++ {
		snapshot.Rules = append(snapshot.Rules, PolicyRule{
			Type: "g",
			V0:   fmt.Sprintf("role-%d", depth),
			V1:   fmt.Sprintf("role-%d", depth+1),
		})
	}
	runtime, err := NewRuntime(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for action, want := range map[string]bool{"within": true, "beyond": false} {
		decision, err := runtime.Authorize(context.Background(), authorization.Request{
			Principal: authentication.Principal{
				Authenticated:      true,
				Subject:            "user",
				AuthenticatedUntil: time.Now().UTC().Add(time.Minute),
			},
			Resource: "document",
			Action:   action,
		})
		if err != nil || decision.Allowed != want {
			t.Fatalf("Authorize(%s) = %#v, %v; want allowed=%t", action, decision, err, want)
		}
	}
}

func TestPrepareCancellationAndCallerMutation(t *testing.T) {
	t.Parallel()

	runtime, err := NewRuntime(internalSnapshot(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Prepare(nil, internalSnapshot(2)); !errors.Is(
		err,
		ErrInvalidSnapshot,
	) {
		t.Fatalf("Prepare(nil) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Prepare(cancelled, internalSnapshot(2)); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Prepare(cancelled) error = %v", err)
	}
	if _, err := runtime.Prepare(
		context.Background(),
		internalSnapshot(1),
	); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("Prepare(stale) error = %v", err)
	}

	candidate := internalSnapshot(2)
	candidate.Rules[0].V2 = "write"
	prepared, err := runtime.Prepare(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Rules[0].V2 = "delete"
	runtime.SetDesiredRevision(2)
	if err := runtime.Publish(prepared); err != nil {
		t.Fatal(err)
	}
	decision, err := runtime.Authorize(
		context.Background(),
		internalRequest("write"),
	)
	if err != nil || !decision.Allowed || decision.Revision != 2 {
		t.Fatalf("Authorize(immutable prepared) = %#v, %v", decision, err)
	}
}

func TestNilRuntimeAndContextFailClosed(t *testing.T) {
	t.Parallel()

	var runtime *Runtime
	if runtime.AppliedRevision() != 0 {
		t.Fatal("nil AppliedRevision() != 0")
	}
	if runtime.DesiredRevision() != 0 {
		t.Fatal("nil DesiredRevision() != 0")
	}
	runtime.SetDesiredRevision(1)
	if err := runtime.Healthy(); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("nil Healthy() error = %v", err)
	}
	if _, err := runtime.Prepare(
		context.Background(),
		internalSnapshot(2),
	); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("nil Prepare() error = %v", err)
	}
	if err := runtime.Publish(nil); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("nil Publish() error = %v", err)
	}
	decision, err := runtime.Authorize(context.Background(), internalRequest("read"))
	if !errors.Is(err, authorization.ErrInvalidRequest) ||
		decision.Allowed ||
		decision.Reason != authorization.ReasonInvalidRequest {
		t.Fatalf("nil Authorize() = %#v, %v", decision, err)
	}

	runtime, err = NewRuntime(internalSnapshot(1))
	if err != nil {
		t.Fatal(err)
	}
	decision, err = runtime.Authorize(nil, internalRequest("read"))
	if !errors.Is(err, authorization.ErrInvalidRequest) ||
		decision.Allowed {
		t.Fatalf("Authorize(nil context) = %#v, %v", decision, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err = runtime.Authorize(cancelled, internalRequest("read"))
	if !errors.Is(err, context.Canceled) ||
		!errors.Is(err, authorization.ErrEvaluation) ||
		decision.Allowed ||
		decision.Reason != authorization.ReasonEvaluationError {
		t.Fatalf("Authorize(cancelled) = %#v, %v", decision, err)
	}

	uninitialized := &Runtime{options: defaultRuntimeOptions()}
	decision, err = uninitialized.Authorize(context.Background(), internalRequest("read"))
	if !errors.Is(err, authorization.ErrEvaluation) || decision.Allowed ||
		decision.Reason != authorization.ReasonEvaluationError {
		t.Fatalf("Authorize(uninitialized runtime) = %#v, %v", decision, err)
	}
}

func TestPolicyAndModelValidationBranches(t *testing.T) {
	t.Parallel()

	valid := internalSnapshot(1)
	tests := []Snapshot{
		{Revision: 0, Model: internalRBACModel},
		{Revision: 1, Model: strings.Repeat("x", defaultMaxModelBytes+1)},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules: []PolicyRule{
				{Type: "p/bad", V0: "a", V1: "b", V2: "c"},
			},
		},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules: []PolicyRule{
				{Type: strings.Repeat("p", maxPolicyTypeBytes+1), V0: "a", V1: "b", V2: "c"},
			},
		},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules: []PolicyRule{
				{Type: "p", V0: " role", V1: "document", V2: "read"},
			},
		},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules: []PolicyRule{
				{Type: "p", V0: "role\n", V1: "document", V2: "read"},
			},
		},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules: []PolicyRule{
				{Type: "p", V0: "ro\nle", V1: "document", V2: "read"},
			},
		},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules: []PolicyRule{
				{Type: "p", V0: strings.Repeat("x", defaultMaxValueBytes+1), V1: "document", V2: "read"},
			},
		},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules: []PolicyRule{
				{Type: "p", V0: "role", V1: "document", V2: "read", V3: "extra"},
			},
		},
		{
			Revision: 1,
			Model: `
[request_definition]
r = sub, obj, act
[policy_definition]
p = v0, v1, v2, v3, v4, v5, v6
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = true
`,
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"[request_definition]\nr = sub, obj, act\n", "", 1),
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"[policy_definition]\np = sub, obj, act\n", "", 1),
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"r = sub, obj, act", "r2 = sub, obj, act", 1),
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"r = sub, obj, act", "r = sub, obj", 1),
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"g = _, _", "g = v0, v1, v2, v3, v4, v5, v6", 1),
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"[role_definition]\ng = _, _\n", "", 1),
			Rules: []PolicyRule{{Type: "g", V0: "user", V1: "role"}},
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"p = sub, obj, act", "p = sub, obj, act, extra, extra2, extra3, extra4", 1),
		},
		{
			Revision: 1,
			Model: strings.Replace(internalRBACModel,
				"g = _, _", "g = user, role", 1),
			Rules: []PolicyRule{{Type: "g", V0: "user", V1: "role"}},
		},
		{
			Revision: 1,
			Model:    DefaultModel(),
			Rules: []PolicyRule{
				{Type: "g3", V0: "scope:a", V1: "scope:a"},
			},
		},
		{
			Revision: 1,
			Model:    DefaultModel(),
			Rules: []PolicyRule{
				{Type: "g3", V0: "scope:a", V1: "scope:b"},
				{Type: "g3", V0: "scope:a", V1: "scope:b"},
			},
		},
		{
			Revision: 1,
			Model:    DefaultModel(),
			Rules: []PolicyRule{
				{Type: "g3", V0: "scope:a", V1: "scope:b"},
				{Type: "g3", V0: "scope:b", V1: "scope:a"},
			},
		},
		{
			Revision: 1,
			Model:    internalRBACModel,
			Rules:    []PolicyRule{{Type: "p2", V0: "role", V1: "document", V2: "read"}},
		},
	}
	for index, candidate := range tests {
		if _, err := NewRuntime(candidate); !errors.Is(err, ErrInvalidSnapshot) {
			t.Errorf("case %d NewRuntime() error = %v", index, err)
		}
	}

	limits := defaultRuntimeOptions().limits
	limits.MaxRules = 1
	if _, err := prepareLiveSnapshot(
		context.Background(),
		valid,
		limits,
	); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("rule limit error = %v", err)
	}
	if _, err := prepareLiveSnapshot(
		nil,
		valid,
		defaultRuntimeOptions().limits,
	); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("nil context error = %v", err)
	}

	runtime, err := NewRuntime(valid)
	if err != nil {
		t.Fatal(err)
	}
	cancelDuringRuleLoad := &cancelOnErrorCheck{Context: context.Background(), cancelAt: 3}
	if _, err := runtime.Prepare(cancelDuringRuleLoad, internalSnapshot(2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare(cancel during rule load) error = %v", err)
	}
	cancelAfterBuild := &cancelOnErrorCheck{Context: context.Background(), cancelAt: 2}
	if _, err := runtime.Prepare(cancelAfterBuild, Snapshot{Revision: 2, Model: internalRBACModel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare(cancel after role links) error = %v", err)
	}
}

func TestScopeHierarchyRejectsMalformedAndOverDepthPolicyGraphs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rules []PolicyRule
	}{
		{name: "missing child", rules: []PolicyRule{{Type: "g3", V1: "scope:parent"}}},
		{name: "missing parent", rules: []PolicyRule{{Type: "g3", V0: "scope:child"}}},
		{name: "unexpected policy field", rules: []PolicyRule{{Type: "g3", V0: "scope:child", V1: "scope:parent", V2: "extra"}}},
		{name: "hierarchy exceeds depth limit", rules: deepScopeHierarchy(DefaultMaxScopeHierarchyDepth + 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewRuntime(Snapshot{Revision: 1, Model: DefaultModel(), Rules: test.rules})
			if !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("NewRuntime() error = %v", err)
			}
		})
	}
}

func TestSnapshotFenceRejectsUnpublishedSnapshots(t *testing.T) {
	t.Parallel()

	runtime, err := NewRuntime(internalSnapshot(1))
	if err != nil {
		t.Fatal(err)
	}
	current := runtime.current()
	if !runtime.snapshotIsCurrent(current) {
		t.Fatal("snapshotIsCurrent(current) = false")
	}
	if runtime.snapshotIsCurrent(nil) {
		t.Fatal("snapshotIsCurrent(nil) = true")
	}
	if runtime.snapshotIsCurrent(&liveSnapshot{revision: 1}) {
		t.Fatal("snapshotIsCurrent(foreign snapshot) = true")
	}
	runtime.SetDesiredRevision(2)
	if runtime.snapshotIsCurrent(current) {
		t.Fatal("snapshotIsCurrent(stale revision) = true")
	}
}

func deepScopeHierarchy(depth int) []PolicyRule {
	rules := make([]PolicyRule, 0, depth)
	for index := 0; index < depth; index++ {
		rules = append(rules, PolicyRule{
			Type: "g3",
			V0:   fmt.Sprintf("scope:%d", index),
			V1:   fmt.Sprintf("scope:%d", index+1),
		})
	}
	return rules
}

type cancelOnErrorCheck struct {
	context.Context
	cancelAt int
	checks   int
}

func (ctx *cancelOnErrorCheck) Err() error {
	ctx.checks++
	if ctx.checks >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}

func internalSnapshot(revision int64) Snapshot {
	return Snapshot{
		Revision: revision,
		Model:    internalRBACModel,
		Rules: []PolicyRule{
			{Type: "p", V0: "role:reader", V1: "document", V2: "read"},
			{Type: "g", V0: "user:1", V1: "role:reader"},
		},
	}
}

func internalRequest(action string) authorization.Request {
	now := time.Now()
	return authorization.Request{
		Principal: authentication.Principal{
			Authenticated:      true,
			Subject:            "user:1",
			UserID:             "1",
			SessionID:          "session-1",
			TokenID:            "token-1",
			AuthenticatedUntil: now.Add(time.Minute),
		},
		Resource: "document",
		Action:   action,
	}
}
