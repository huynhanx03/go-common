package security_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

func TestCheckerAuthenticatesAndAuthorizesScopedAlternatives(t *testing.T) {
	now, principal := testPrincipal()
	verifyCalls, authorizeCalls := 0, 0
	permission := security.Permission{
		Scope:    authorization.Scope{Type: "tenant", ID: "community"},
		Resource: "problems", ResourceID: "problem-1", Action: "update",
	}
	checker := security.NewChecker(security.Config{
		Clock: func() time.Time { return now },
		Verifier: verifierFunc(func(_ context.Context, raw string, purpose authentication.TokenPurpose) (authentication.Principal, error) {
			verifyCalls++
			if raw != "token" || purpose != authentication.PurposeAccess {
				t.Fatalf("credential = %q, %q", raw, purpose)
			}
			return principal, nil
		}),
		Authorizer: authorizerFunc(func(_ context.Context, request authorization.Request) (authorization.Decision, error) {
			authorizeCalls++
			if request.Principal != principal || request.Scope != permission.Scope || request.ResourceID != permission.ResourceID {
				t.Fatalf("request lost identity/scope/object: %+v", request)
			}
			return authorization.Decision{Allowed: request.Action == "update", Reason: authorization.ReasonAllowed, Revision: 7}, nil
		}),
	})
	denied := permission
	denied.Action = "manage"
	result, err := checker.Check(context.Background(), security.Request{
		Token: "token", Purpose: authentication.PurposeAccess,
		Permissions: []security.Permission{denied, permission, permission},
	})
	if err != nil || result.Principal != principal || !result.Decision.Allowed || result.Decision.Revision != 7 {
		t.Fatalf("Check() = %+v, %v", result, err)
	}
	if verifyCalls != 1 || authorizeCalls != 2 {
		t.Fatalf("verify calls = %d; authorize calls = %d", verifyCalls, authorizeCalls)
	}
}

func TestCheckerRejectsCredentialsBeforeAuthorization(t *testing.T) {
	now, principal := testPrincipal()
	expired := principal
	expired.AuthenticatedUntil = now
	for _, test := range []struct {
		name        string
		token       string
		purpose     authentication.TokenPurpose
		principal   authentication.Principal
		verifyError error
		want        error
	}{
		{name: "missing token", purpose: authentication.PurposeAccess, want: security.ErrUnauthenticated},
		{name: "bad purpose", token: "token", purpose: "unknown", want: security.ErrInvalidRequest},
		{name: "invalid token", token: "token", purpose: authentication.PurposeAccess, verifyError: authentication.ErrInvalidToken, want: security.ErrUnauthenticated},
		{name: "anonymous verifier result", token: "token", purpose: authentication.PurposeAccess, want: security.ErrUnauthenticated},
		{name: "expired principal", token: "token", purpose: authentication.PurposeAccess, principal: expired, want: security.ErrUnauthenticated},
	} {
		t.Run(test.name, func(t *testing.T) {
			checker := security.NewChecker(security.Config{
				Clock: func() time.Time { return now },
				Verifier: verifierFunc(func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
					return test.principal, test.verifyError
				}),
				Authorizer: authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
					t.Fatal("authorizer called after failed authentication")
					return authorization.Decision{}, nil
				}),
			})
			result, err := checker.Check(context.Background(), security.Request{Token: test.token, Purpose: test.purpose})
			if !errors.Is(err, test.want) || result.Principal.Authenticated || result.Decision.Allowed {
				t.Fatalf("Check() = %+v, %v; want %v", result, err, test.want)
			}
		})
	}
}

func TestCheckerValidatesAllPermissionsBeforeEvaluating(t *testing.T) {
	now, principal := testPrincipal()
	valid := security.Permission{Resource: "problems", Action: "read"}
	for name, permissions := range map[string][]security.Permission{
		"empty":                     nil,
		"invalid later alternative": {valid, {Resource: "", Action: "read"}},
		"invalid scope":             {{Scope: authorization.Scope{Type: "tenant"}, Resource: "problems", Action: "read"}},
		"invalid object":            {{Resource: "problems", ResourceID: "bad\x00id", Action: "read"}},
		"invalid action":            {{Resource: "problems", Action: " read"}},
	} {
		t.Run(name, func(t *testing.T) {
			checker := security.NewChecker(security.Config{
				Clock: func() time.Time { return now },
				Authorizer: authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
					t.Fatal("invalid alternatives reached authorizer")
					return authorization.Decision{Allowed: true}, nil
				}),
			})
			decision, err := checker.Authorize(context.Background(), principal, permissions...)
			if !errors.Is(err, security.ErrInvalidRequest) || decision.Allowed {
				t.Fatalf("Authorize() = %+v, %v", decision, err)
			}
		})
	}
}

func TestCheckerPreservesDenialsAndStopsOnEvaluatorFailure(t *testing.T) {
	now, principal := testPrincipal()
	permission := security.Permission{Resource: "problems", Action: "read"}
	for _, evaluatorError := range []error{nil, errors.New("policy backend unavailable")} {
		calls := 0
		checker := security.NewChecker(security.Config{
			Clock: func() time.Time { return now },
			Verifier: verifierFunc(func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
				return principal, nil
			}),
			Authorizer: authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
				calls++
				if calls > 1 && evaluatorError != nil {
					return authorization.Decision{Allowed: true}, nil
				}
				return authorization.Decision{Allowed: evaluatorError != nil, Reason: authorization.ReasonPolicyDenied, Revision: 9}, evaluatorError
			}),
		})
		result, err := checker.Check(context.Background(), security.Request{
			Token: "token", Purpose: authentication.PurposeAccess, Permissions: []security.Permission{permission, permission},
		})
		want := security.ErrForbidden
		if evaluatorError != nil {
			want = authorization.ErrEvaluation
			if !errors.Is(err, evaluatorError) {
				t.Fatalf("evaluator cause was lost: %v", err)
			}
			if calls != 1 {
				t.Fatalf("evaluation continued after an error: %d calls", calls)
			}
		}
		if !errors.Is(err, want) || result.Decision.Allowed || result.Principal.Authenticated || result.Decision.Revision != 9 {
			t.Fatalf("Check() = %+v, %v", result, err)
		}
	}
}

func TestCheckerHonorsCancellationAtEachStage(t *testing.T) {
	now, principal := testPrincipal()
	for _, stage := range []string{"before", "verify", "authorize"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "before" {
				cancel()
			}
			checker := security.NewChecker(security.Config{
				Clock: func() time.Time { return now },
				Verifier: verifierFunc(func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
					if stage == "verify" {
						cancel()
					}
					return principal, nil
				}),
				Authorizer: authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
					if stage == "authorize" {
						cancel()
					}
					return authorization.Decision{Allowed: true}, nil
				}),
			})
			result, err := checker.Check(ctx, security.Request{
				Token: "token", Purpose: authentication.PurposeAccess,
				Permissions: []security.Permission{{Resource: "problems", Action: "read"}},
			})
			if !errors.Is(err, context.Canceled) || result.Principal.Authenticated || result.Decision.Allowed {
				t.Fatalf("Check() = %+v, %v", result, err)
			}
		})
	}
}

func TestCheckerRejectsMissingDependenciesAndInvalidContexts(t *testing.T) {
	_, principal := testPrincipal()
	var nilVerifier verifierFunc
	var nilAuthorizer authorizerFunc
	for _, checker := range []*security.Checker{nil, security.NewChecker(security.Config{Verifier: nilVerifier, Authorizer: nilAuthorizer})} {
		if _, err := checker.Authenticate(context.Background(), "token", authentication.PurposeAccess); !errors.Is(err, security.ErrInvalidConfiguration) {
			t.Fatalf("Authenticate() error = %v", err)
		}
		if _, err := checker.Authorize(context.Background(), principal); !errors.Is(err, security.ErrInvalidConfiguration) {
			t.Fatalf("Authorize() error = %v", err)
		}
	}
	checker := security.NewChecker(security.Config{Verifier: valueVerifier{principal}, Authorizer: authorizerFunc(nil)})
	if got, err := checker.Authenticate(context.Background(), "token", authentication.PurposeAccess); err != nil || got != principal {
		t.Fatalf("Authenticate(default clock) = %+v, %v", got, err)
	}
	if _, err := checker.Authenticate(nil, "token", authentication.PurposeAccess); !errors.Is(err, security.ErrInvalidRequest) {
		t.Fatalf("Authenticate(nil context) error = %v", err)
	}
	if _, err := checker.Authorize(nil, principal); !errors.Is(err, security.ErrInvalidRequest) {
		t.Fatalf("Authorize(nil context) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.Authorize(ctx, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("Authorize(canceled context) error = %v", err)
	}
}

func TestCheckerAuthorizationRequiresAuthenticatedPrincipal(t *testing.T) {
	now, principal := testPrincipal()
	principal.AuthenticatedUntil = now
	checker := security.NewChecker(security.Config{
		Clock: func() time.Time { return now },
		Authorizer: authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
			t.Fatal("invalid principal reached evaluator")
			return authorization.Decision{}, nil
		}),
	})
	for _, value := range []authentication.Principal{authentication.Anonymous(), principal} {
		if _, err := checker.Authorize(context.Background(), value); !errors.Is(err, security.ErrUnauthenticated) {
			t.Fatalf("Authorize() error = %v", err)
		}
	}
}

func TestCheckerContextPrincipalBoundary(t *testing.T) {
	now, principal := testPrincipal()
	checker := security.NewChecker(security.Config{Clock: func() time.Time { return now }})
	ctx := authentication.WithPrincipal(context.Background(), principal)
	if got, err := checker.Principal(ctx); err != nil || got != principal {
		t.Fatalf("Principal() = %+v, %v", got, err)
	}
	for _, missing := range []context.Context{nil, context.Background()} {
		if _, err := checker.Principal(missing); !errors.Is(err, security.ErrUnauthenticated) {
			t.Fatalf("Principal(missing) error = %v", err)
		}
	}
	var absent *security.Checker
	if _, err := absent.Principal(ctx); !errors.Is(err, security.ErrInvalidConfiguration) {
		t.Fatalf("Principal(nil checker) error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := checker.Principal(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Principal(canceled) error = %v", err)
	}
}

type verifierFunc func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error)

func (f verifierFunc) Verify(ctx context.Context, raw string, purpose authentication.TokenPurpose) (authentication.Principal, error) {
	return f(ctx, raw, purpose)
}

type authorizerFunc func(context.Context, authorization.Request) (authorization.Decision, error)

func (f authorizerFunc) Authorize(ctx context.Context, request authorization.Request) (authorization.Decision, error) {
	return f(ctx, request)
}

type valueVerifier struct{ principal authentication.Principal }

func (v valueVerifier) Verify(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
	return v.principal, nil
}

func testPrincipal() (time.Time, authentication.Principal) {
	now := time.Now().UTC()
	return now, authentication.Principal{Authenticated: true, Subject: "user:42", AuthenticatedUntil: now.Add(time.Hour)}
}
