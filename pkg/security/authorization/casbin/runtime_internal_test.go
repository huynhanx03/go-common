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
		{MaxModelBytes: hardMaxModelBytes + 1, MaxRules: 1, MaxValueBytes: 1},
		{MaxModelBytes: 1, MaxRules: hardMaxRules + 1, MaxValueBytes: 1},
		{MaxModelBytes: 1, MaxRules: 1, MaxValueBytes: hardMaxValueBytes + 1},
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
