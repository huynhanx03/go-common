package casbin_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
	authcasbin "github.com/huynhanx03/go-common/pkg/security/authorization/casbin"
)

const rbacModel = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && (p.obj == "*" || r.obj == p.obj) && (p.act == "*" || r.act == p.act)
`

func TestRuntimeFailsClosedAndUsesClosedReasons(t *testing.T) {
	t.Parallel()

	runtime := newRuntime(t, snapshot(1, "read"))
	now := fixedNow()

	allowed, err := runtime.Authorize(context.Background(), request(now, "read"))
	if err != nil || !allowed.Allowed ||
		allowed.Reason != authorization.ReasonAllowed ||
		allowed.Revision != 1 {
		t.Fatalf("Authorize(allow) = %#v, %v", allowed, err)
	}

	denied, err := runtime.Authorize(context.Background(), request(now, "write"))
	if err != nil || denied.Allowed ||
		denied.Reason != authorization.ReasonPolicyDenied ||
		denied.Revision != 1 {
		t.Fatalf("Authorize(deny) = %#v, %v", denied, err)
	}

	anonymousRequest := request(now, "read")
	anonymousRequest.Principal = authentication.Anonymous()
	anonymous, err := runtime.Authorize(context.Background(), anonymousRequest)
	if err != nil || anonymous.Allowed ||
		anonymous.Reason != authorization.ReasonAnonymous {
		t.Fatalf("Authorize(anonymous) = %#v, %v", anonymous, err)
	}

	malformedRequest := request(now, "read")
	malformedRequest.Resource = ""
	malformed, err := runtime.Authorize(context.Background(), malformedRequest)
	if !errors.Is(err, authorization.ErrInvalidRequest) ||
		malformed.Allowed ||
		malformed.Reason != authorization.ReasonInvalidRequest {
		t.Fatalf("Authorize(malformed) = %#v, %v", malformed, err)
	}
}

func TestPrepareDoesNotMutateAndPublishIsAtomic(t *testing.T) {
	t.Parallel()

	runtime := newRuntime(t, snapshot(1, "read"))
	prepared, err := runtime.Prepare(context.Background(), snapshot(2, "write"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.AppliedRevision() != 1 {
		t.Fatalf("revision after Prepare = %d", runtime.AppliedRevision())
	}
	before, err := runtime.Authorize(context.Background(), request(fixedNow(), "read"))
	if err != nil || !before.Allowed || before.Revision != 1 {
		t.Fatalf("old snapshot changed during prepare: %#v, %v", before, err)
	}

	var stop atomic.Bool
	var mixed atomic.Int64
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for !stop.Load() {
				for _, action := range []string{"read", "write"} {
					decision, authErr := runtime.Authorize(
						context.Background(),
						request(fixedNow(), action),
					)
					if authErr != nil {
						if errors.Is(authErr, authcasbin.ErrUnhealthy) &&
							!decision.Allowed &&
							decision.Reason == authorization.ReasonEvaluationError {
							continue
						}
						mixed.Add(1)
						continue
					}
					valid := (decision.Revision == 1 &&
						action == "read" &&
						decision.Allowed) ||
						(decision.Revision == 1 &&
							action == "write" &&
							!decision.Allowed) ||
						(decision.Revision == 2 &&
							action == "read" &&
							!decision.Allowed) ||
						(decision.Revision == 2 &&
							action == "write" &&
							decision.Allowed)
					if !valid {
						mixed.Add(1)
					}
				}
			}
		}()
	}
	time.Sleep(5 * time.Millisecond)
	runtime.SetDesiredRevision(2)
	if err := runtime.Publish(prepared); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	stop.Store(true)
	wait.Wait()
	if got := mixed.Load(); got != 0 {
		t.Fatalf("mixed snapshot observations = %d", got)
	}
	if runtime.AppliedRevision() != 2 {
		t.Fatalf("AppliedRevision() = %d", runtime.AppliedRevision())
	}
	if err := runtime.Healthy(); err != nil {
		t.Fatalf("Healthy() after publish = %v", err)
	}
}

func TestPreparedSnapshotOwnershipAndSingleUse(t *testing.T) {
	t.Parallel()

	first := newRuntime(t, snapshot(1, "read"))
	second := newRuntime(t, snapshot(1, "read"))
	prepared, err := first.Prepare(context.Background(), snapshot(2, "write"))
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Publish(prepared); !errors.Is(err, authcasbin.ErrForeignPrepared) {
		t.Fatalf("foreign Publish() error = %v", err)
	}
	if err := first.Publish(prepared); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := first.Publish(prepared); !errors.Is(err, authcasbin.ErrAlreadyPublished) {
		t.Fatalf("double Publish() error = %v", err)
	}

	stale, err := first.Prepare(context.Background(), snapshot(3, "delete"))
	if err != nil {
		t.Fatal(err)
	}
	newer, err := first.Prepare(context.Background(), snapshot(4, "update"))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Publish(newer); err != nil {
		t.Fatal(err)
	}
	if err := first.Publish(stale); !errors.Is(err, authcasbin.ErrStaleRevision) {
		t.Fatalf("stale Publish() error = %v", err)
	}
}

func TestDesiredAppliedHealth(t *testing.T) {
	t.Parallel()

	runtime := newRuntime(t, snapshot(7, "read"))
	if err := runtime.Healthy(); err != nil {
		t.Fatalf("Healthy() error = %v", err)
	}
	runtime.SetDesiredRevision(8)
	if runtime.DesiredRevision() != 8 {
		t.Fatalf("DesiredRevision() = %d", runtime.DesiredRevision())
	}
	if err := runtime.Healthy(); !errors.Is(err, authcasbin.ErrUnhealthy) {
		t.Fatalf("Healthy(stale) error = %v", err)
	}
	decision, err := runtime.Authorize(context.Background(), request(fixedNow(), "read"))
	if decision.Allowed || decision.Reason != authorization.ReasonEvaluationError ||
		!errors.Is(err, authcasbin.ErrUnhealthy) ||
		!errors.Is(err, authorization.ErrEvaluation) {
		t.Fatalf("Authorize(stale) = %#v, %v", decision, err)
	}
	prepared, err := runtime.Prepare(context.Background(), snapshot(8, "write"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Publish(prepared); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Healthy(); err != nil {
		t.Fatalf("Healthy(after publish) error = %v", err)
	}
}

func TestDesiredRevisionCompareAndSwapDoesNotClobberNewerFence(t *testing.T) {
	t.Parallel()

	runtime := newRuntime(t, snapshot(3, "read"))
	if runtime.CompareAndSwapDesiredRevision(2, 1) {
		t.Fatal("CompareAndSwapDesiredRevision() accepted a stale expected revision")
	}
	if !runtime.CompareAndSwapDesiredRevision(3, 4) {
		t.Fatal("CompareAndSwapDesiredRevision() rejected the current revision")
	}
	if runtime.DesiredRevision() != 4 {
		t.Fatalf("DesiredRevision() = %d, want 4", runtime.DesiredRevision())
	}
	if runtime.CompareAndSwapDesiredRevision(4, 0) {
		t.Fatal("CompareAndSwapDesiredRevision() accepted an invalid new revision")
	}

	var nilRuntime *authcasbin.Runtime
	if nilRuntime.CompareAndSwapDesiredRevision(1, 2) {
		t.Fatal("nil CompareAndSwapDesiredRevision() = true")
	}
}

func TestEvaluationErrorNeverAllows(t *testing.T) {
	t.Parallel()

	brokenModel := `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = undefined_function(r.sub)
`
	runtime := newRuntime(t, authcasbin.Snapshot{
		Revision: 1,
		Model:    brokenModel,
	})
	decision, err := runtime.Authorize(
		context.Background(),
		request(fixedNow(), "read"),
	)
	if !errors.Is(err, authorization.ErrEvaluation) ||
		decision.Allowed ||
		decision.Reason != authorization.ReasonEvaluationError {
		t.Fatalf("Authorize(evaluator error) = %#v, %v", decision, err)
	}
}

func TestSnapshotValidation(t *testing.T) {
	t.Parallel()

	tests := []authcasbin.Snapshot{
		{},
		{Revision: 1, Model: "not a model"},
		{
			Revision: 1,
			Model:    rbacModel,
			Rules: []authcasbin.PolicyRule{
				{Type: "unknown", V0: "x"},
			},
		},
		{
			Revision: 1,
			Model:    rbacModel,
			Rules: []authcasbin.PolicyRule{
				{Type: "p", V0: "role:reader", V1: "document"},
			},
		},
		{
			Revision: 1,
			Model:    rbacModel,
			Rules: []authcasbin.PolicyRule{
				{Type: "p", V0: "role:reader", V1: "document", V2: "read"},
				{Type: "p", V0: "role:reader", V1: "document", V2: "read"},
			},
		},
	}
	for index, candidate := range tests {
		if _, err := authcasbin.NewRuntime(candidate); !errors.Is(
			err,
			authcasbin.ErrInvalidSnapshot,
		) {
			t.Errorf("case %d NewRuntime() error = %v", index, err)
		}
	}
}

func newRuntime(t *testing.T, initial authcasbin.Snapshot) *authcasbin.Runtime {
	t.Helper()

	runtime, err := authcasbin.NewRuntime(
		initial,
		authcasbin.WithClock(fixedNow),
	)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func snapshot(revision int64, action string) authcasbin.Snapshot {
	return authcasbin.Snapshot{
		Revision: revision,
		Model:    rbacModel,
		Rules: []authcasbin.PolicyRule{
			{
				Type: "p",
				V0:   "role:reader",
				V1:   "document",
				V2:   action,
			},
			{
				Type: "g",
				V0:   "user:1",
				V1:   "role:reader",
			},
		},
	}
}

func request(now time.Time, action string) authorization.Request {
	return authorization.Request{
		Principal: authentication.Principal{
			Authenticated:      true,
			Subject:            "user:1",
			UserID:             "1",
			SessionID:          "session-1",
			TokenID:            "token-1",
			AuthenticatedUntil: now.Add(time.Hour),
		},
		Resource: "document",
		Action:   action,
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
}
