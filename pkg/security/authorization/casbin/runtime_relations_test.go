package casbin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
	authcasbin "github.com/huynhanx03/go-common/pkg/security/authorization/casbin"
)

const fourFieldScopedModel = `
[request_definition]
r = sub, dom, obj, act
[policy_definition]
p = sub, dom, obj, act
[role_definition]
g = _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && r.obj == p.obj && r.act == p.act
`

func TestDefaultModelAuthorizesInheritedRolesWithinScopeHierarchy(t *testing.T) {
	t.Parallel()

	model := authcasbin.DefaultModel()
	if model == "" || authcasbin.SharedModel() != model {
		t.Fatal("shared model compatibility name does not return the default model")
	}
	if authcasbin.SystemScope() != authorization.DefaultSystemScope() {
		t.Fatal("system scope compatibility name does not return the default scope")
	}

	snapshot := authcasbin.Snapshot{
		Revision: 1,
		Model:    model,
		Rules: []authcasbin.PolicyRule{
			{Type: "p", V0: "role:senior", V1: "tenant:acme", V2: "document", V3: "read"},
			{Type: "g", V0: "role:junior", V1: "role:senior", V2: "tenant:acme"},
			{Type: "g", V0: "user:1", V1: "role:junior", V2: "tenant:acme"},
			{Type: "g3", V0: "project:alpha", V1: "tenant:acme"},
		},
	}
	runtime, err := authcasbin.NewRuntime(snapshot, authcasbin.WithClock(fixedNow))
	if err != nil {
		t.Fatal(err)
	}

	request := request(fixedNow(), "read")
	request.Scope = authorization.Scope{Type: "project", ID: "alpha"}
	decision, err := runtime.Authorize(context.Background(), request)
	if err != nil || !decision.Allowed || decision.Revision != 1 {
		t.Fatalf("Authorize(inherited role in descendant scope) = %#v, %v", decision, err)
	}

	request.Scope.ID = "unrelated"
	decision, err = runtime.Authorize(context.Background(), request)
	if err != nil || decision.Allowed {
		t.Fatalf("Authorize(unrelated scope) = %#v, %v; want denied", decision, err)
	}
}

func TestDefaultModelTerminatesWhenScopeHierarchyHasSharedAncestors(t *testing.T) {
	t.Parallel()

	runtime, err := authcasbin.NewRuntime(authcasbin.Snapshot{
		Revision: 1,
		Model:    authcasbin.DefaultModel(),
		Rules: []authcasbin.PolicyRule{
			{Type: "p", V0: "role:reader", V1: "scope:missing", V2: "document", V3: "read"},
			{Type: "g", V0: "user:1", V1: "role:reader", V2: "scope:child"},
			{Type: "g3", V0: "scope:child", V1: "scope:left"},
			{Type: "g3", V0: "scope:child", V1: "scope:right"},
			{Type: "g3", V0: "scope:left", V1: "scope:root"},
			{Type: "g3", V0: "scope:right", V1: "scope:root"},
		},
	}, authcasbin.WithClock(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	authRequest := request(fixedNow(), "read")
	authRequest.Scope = authorization.Scope{Type: "scope", ID: "child"}
	decision, err := runtime.Authorize(context.Background(), authRequest)
	if err != nil || decision.Allowed {
		t.Fatalf("Authorize(non-ancestor permission) = %#v, %v; want denied", decision, err)
	}
}

func TestScopeMatcherRejectsInvalidModelArgumentsAtEvaluation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model string
	}{
		{
			name:  "wrong arity",
			model: strings.Replace(authcasbin.DefaultModel(), "scopeMatch(r.dom, p.dom)", "scopeMatch(r.dom)", 1),
		},
		{
			name:  "non-string argument",
			model: strings.Replace(authcasbin.DefaultModel(), "scopeMatch(r.dom, p.dom)", "scopeMatch(1, p.dom)", 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runtime := newRuntime(t, authcasbin.Snapshot{
				Revision: 1,
				Model:    test.model,
				Rules: []authcasbin.PolicyRule{
					{Type: "p", V0: "role:reader", V1: "tenant:acme", V2: "document", V3: "read"},
					{Type: "g", V0: "user:1", V1: "role:reader", V2: "tenant:acme"},
				},
			})
			authRequest := request(fixedNow(), "read")
			authRequest.Scope = authorization.Scope{Type: "tenant", ID: "acme"}
			decision, err := runtime.Authorize(context.Background(), authRequest)
			if !errors.Is(err, authorization.ErrEvaluation) || decision.Allowed ||
				decision.Reason != authorization.ReasonEvaluationError {
				t.Fatalf("Authorize(invalid scope function) = %#v, %v", decision, err)
			}
		})
	}
}

func TestFourFieldModelUsesScopeAndRequestScopedMembership(t *testing.T) {
	t.Parallel()

	runtime := newRuntime(t, authcasbin.Snapshot{
		Revision: 1,
		Model:    fourFieldScopedModel,
		Rules: []authcasbin.PolicyRule{{
			Type: "p", V0: "role:reader", V1: "tenant:acme", V2: "document", V3: "read",
		}},
	})
	authRequest := request(fixedNow(), "read")
	authRequest.Scope = authorization.Scope{Type: "tenant", ID: "acme"}
	decision, err := runtime.AuthorizeWithRelations(context.Background(), authRequest, []authcasbin.PolicyRule{{
		Type: "g", V0: authRequest.Principal.Subject, V1: "role:reader", V2: authRequest.Scope.Key(),
	}})
	if err != nil || !decision.Allowed {
		t.Fatalf("AuthorizeWithRelations(four-field model) = %#v, %v", decision, err)
	}

	missingScope := authRequest
	missingScope.Scope = authorization.Scope{}
	decision, err = runtime.Authorize(context.Background(), missingScope)
	if !errors.Is(err, authorization.ErrInvalidRequest) || decision.Allowed {
		t.Fatalf("Authorize(missing scoped-model domain) = %#v, %v", decision, err)
	}
}

func TestAuthorizeWithRelationsUsesRequestScopedMembership(t *testing.T) {
	t.Parallel()

	snapshot := authcasbin.Snapshot{
		Revision: 1,
		Model:    authcasbin.DefaultModel(),
		Rules: []authcasbin.PolicyRule{
			{Type: "p", V0: "role:senior", V1: "tenant:acme", V2: "document", V3: "read"},
			{Type: "g", V0: "role:junior", V1: "role:senior", V2: "tenant:acme"},
		},
	}
	runtime, err := authcasbin.NewRuntime(snapshot, authcasbin.WithClock(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	request := request(fixedNow(), "read")
	request.Scope = authorization.Scope{Type: "tenant", ID: "acme"}
	relations := []authcasbin.PolicyRule{{
		Type: "g",
		V0:   request.Principal.Subject,
		V1:   "role:junior",
		V2:   request.Scope.Key(),
	}}

	decision, err := runtime.AuthorizeWithRelations(context.Background(), request, relations)
	if err != nil || !decision.Allowed || decision.Revision != 1 {
		t.Fatalf("AuthorizeWithRelations() = %#v, %v", decision, err)
	}

	globalRelation := relations[0]
	globalRelation.V1 = "role:senior"
	globalRelation.V2 = authcasbin.SystemScope().Key()
	decision, err = runtime.AuthorizeWithRelations(context.Background(), request, []authcasbin.PolicyRule{globalRelation})
	if err != nil || !decision.Allowed {
		t.Fatalf("AuthorizeWithRelations(system membership) = %#v, %v", decision, err)
	}

	decision, err = runtime.Authorize(context.Background(), request)
	if err != nil || decision.Allowed {
		t.Fatalf("Authorize() after request = %#v, %v; temporary membership leaked", decision, err)
	}
}

func TestAuthorizeWithRelationsSupportsResourceRelationships(t *testing.T) {
	t.Parallel()

	runtime, err := authcasbin.NewRuntime(authcasbin.Snapshot{
		Revision: 1,
		Model:    authcasbin.DefaultModel(),
		Rules: []authcasbin.PolicyRule{
			{Type: "p", V0: "role:editor", V1: "tenant:acme", V2: "document", V3: "edit"},
		},
	}, authcasbin.WithClock(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	request := request(fixedNow(), "edit")
	request.Scope = authorization.Scope{Type: "tenant", ID: "acme"}
	request.ResourceID = "document:42"
	relations := []authcasbin.PolicyRule{{
		Type: "g2",
		V0:   request.Principal.Subject,
		V1:   "role:editor",
		V2:   request.ResourceID,
	}}

	decision, err := runtime.AuthorizeWithRelations(context.Background(), request, relations)
	if err != nil || !decision.Allowed {
		t.Fatalf("AuthorizeWithRelations(resource relation) = %#v, %v", decision, err)
	}
}

func TestAuthorizeWithRelationsRejectsInvalidMembershipRows(t *testing.T) {
	t.Parallel()

	goodRequest := request(fixedNow(), "read")
	goodRequest.Scope = authorization.Scope{Type: "tenant", ID: "acme"}
	goodRequest.ResourceID = "document:42"
	valid := authcasbin.PolicyRule{Type: "g", V0: goodRequest.Principal.Subject, V1: "role:reader", V2: goodRequest.Scope.Key()}
	tests := []struct {
		name    string
		mutate  func(*authcasbin.PolicyRule)
		request func() authorization.Request
	}{
		{name: "foreign subject", mutate: func(rule *authcasbin.PolicyRule) { rule.V0 = "user:other" }},
		{name: "missing role", mutate: func(rule *authcasbin.PolicyRule) { rule.V1 = "" }},
		{name: "invalid role value", mutate: func(rule *authcasbin.PolicyRule) { rule.V1 = " role" }},
		{name: "unexpected extra value", mutate: func(rule *authcasbin.PolicyRule) { rule.V3 = "extra" }},
		{name: "wrong group scope", mutate: func(rule *authcasbin.PolicyRule) { rule.V2 = "tenant:other" }},
		{name: "unknown relation type", mutate: func(rule *authcasbin.PolicyRule) { rule.Type = "g4" }},
		{name: "resource relation without resource ID", mutate: func(rule *authcasbin.PolicyRule) {
			rule.Type = "g2"
			rule.V2 = "document:42"
		}, request: func() authorization.Request {
			request := goodRequest
			request.ResourceID = ""
			return request
		}},
		{name: "resource relation for another resource", mutate: func(rule *authcasbin.PolicyRule) {
			rule.Type = "g2"
			rule.V2 = "document:other"
		}},
		{name: "invalid trailing value", mutate: func(rule *authcasbin.PolicyRule) { rule.V5 = "extra" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := goodRequest
			if test.request != nil {
				request = test.request()
			}
			relation := valid
			test.mutate(&relation)
			runtime := newRuntime(t, authcasbin.Snapshot{
				Revision: 1,
				Model:    authcasbin.DefaultModel(),
				Rules: []authcasbin.PolicyRule{
					{Type: "p", V0: "role:reader", V1: "tenant:acme", V2: "document", V3: "read"},
				},
			})
			if _, err := runtime.AuthorizeWithRelations(context.Background(), request, []authcasbin.PolicyRule{relation}); !errors.Is(err, authcasbin.ErrInvalidRelations) {
				t.Fatalf("AuthorizeWithRelations() error = %v", err)
			}
		})
	}
}

func TestAuthorizeWithRelationsHandlesUnauthenticatedAndUnavailableModels(t *testing.T) {
	t.Parallel()

	runtime := newRuntime(t, authcasbin.Snapshot{Revision: 1, Model: authcasbin.DefaultModel()})
	authRequest := request(fixedNow(), "read")
	authRequest.Scope = authorization.Scope{Type: "tenant", ID: "acme"}

	authRequest.Principal = authentication.Anonymous()
	decision, err := runtime.AuthorizeWithRelations(context.Background(), authRequest, []authcasbin.PolicyRule{{Type: "invalid"}})
	if err != nil || decision.Reason != authorization.ReasonAnonymous || decision.Allowed {
		t.Fatalf("AuthorizeWithRelations(anonymous) = %#v, %v", decision, err)
	}

	authRequest = request(fixedNow(), "read")
	authRequest.Scope = authorization.DefaultSystemScope()
	decision, err = runtime.AuthorizeWithRelations(context.Background(), authRequest, nil)
	if err != nil || decision.Allowed {
		t.Fatalf("AuthorizeWithRelations(no relations) = %#v, %v; want denied", decision, err)
	}

	noGroupModel := strings.Replace(authcasbin.DefaultModel(), "g = _, _, _", "", 1)
	noGroupRuntime := newRuntime(t, authcasbin.Snapshot{Revision: 1, Model: noGroupModel})
	authRequest.Scope = authorization.Scope{Type: "tenant", ID: "acme"}
	decision, err = noGroupRuntime.AuthorizeWithRelations(context.Background(), authRequest, []authcasbin.PolicyRule{{
		Type: "g", V0: authRequest.Principal.Subject, V1: "role:reader", V2: authRequest.Scope.Key(),
	}})
	if !errors.Is(err, authcasbin.ErrInvalidRelations) || decision.Reason != authorization.ReasonEvaluationError {
		t.Fatalf("AuthorizeWithRelations(no group manager) = %#v, %v", decision, err)
	}

	noResourceModel := strings.Replace(authcasbin.DefaultModel(), "g2 = _, _, _", "", 1)
	noResourceRuntime := newRuntime(t, authcasbin.Snapshot{Revision: 1, Model: noResourceModel})
	authRequest.ResourceID = "document:42"
	decision, err = noResourceRuntime.AuthorizeWithRelations(context.Background(), authRequest, []authcasbin.PolicyRule{{
		Type: "g2", V0: authRequest.Principal.Subject, V1: "role:reader", V2: authRequest.ResourceID,
	}})
	if !errors.Is(err, authcasbin.ErrInvalidRelations) || decision.Reason != authorization.ReasonInvalidRequest {
		t.Fatalf("AuthorizeWithRelations(no resource manager) = %#v, %v", decision, err)
	}
}

func TestAuthorizeWithRelationsRejectsInvalidRequestsAndCleansUpAfterCancellation(t *testing.T) {
	t.Parallel()

	runtime, err := authcasbin.NewRuntime(authcasbin.Snapshot{
		Revision: 1,
		Model:    authcasbin.DefaultModel(),
		Rules: []authcasbin.PolicyRule{{
			Type: "p", V0: "role:reader", V1: "tenant:acme", V2: "document", V3: "read",
		}},
	}, authcasbin.WithClock(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	request := request(fixedNow(), "read")
	request.Scope = authorization.Scope{Type: "tenant", ID: "acme"}
	relation := authcasbin.PolicyRule{Type: "g", V0: request.Principal.Subject, V1: "role:reader", V2: request.Scope.Key()}

	invalidRequest := request
	invalidRequest.Scope = authorization.Scope{Type: "Tenant", ID: "acme"}
	decision, err := runtime.AuthorizeWithRelations(context.Background(), invalidRequest, []authcasbin.PolicyRule{relation})
	if !errors.Is(err, authorization.ErrInvalidRequest) || decision.Reason != authorization.ReasonInvalidRequest {
		t.Fatalf("AuthorizeWithRelations(invalid request) = %#v, %v", decision, err)
	}

	cancelAfterEvaluation := &cancelAfterEvaluationContext{Context: context.Background()}
	decision, err = runtime.AuthorizeWithRelations(cancelAfterEvaluation, request, []authcasbin.PolicyRule{relation})
	if !errors.Is(err, context.Canceled) || decision.Allowed {
		t.Fatalf("AuthorizeWithRelations(cancelled) = %#v, %v", decision, err)
	}
	decision, err = runtime.Authorize(context.Background(), request)
	if err != nil || decision.Allowed {
		t.Fatalf("Authorize() after cancelled relation evaluation = %#v, %v; temporary membership leaked", decision, err)
	}
}

func TestNilRuntimeRelationsMethodFailsClosed(t *testing.T) {
	t.Parallel()

	var runtime *authcasbin.Runtime
	decision, err := runtime.AuthorizeWithRelations(context.Background(), request(fixedNow(), "read"), nil)
	if !errors.Is(err, authorization.ErrInvalidRequest) || decision.Allowed {
		t.Fatalf("AuthorizeWithRelations(nil runtime) = %#v, %v", decision, err)
	}
}

type cancelAfterEvaluationContext struct {
	context.Context
	checks int
}

func (ctx *cancelAfterEvaluationContext) Err() error {
	ctx.checks++
	if ctx.checks > 1 {
		return context.Canceled
	}
	return nil
}
