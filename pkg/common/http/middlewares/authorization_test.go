package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

type authorizerFunc func(context.Context, authorization.Request) (authorization.Decision, error)

func (fn authorizerFunc) Authorize(ctx context.Context, request authorization.Request) (authorization.Decision, error) {
	return fn(ctx, request)
}

func serveAuthorization(t *testing.T, principal *authentication.Principal, authorizer authorization.Authorizer) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if principal != nil {
		value := *principal
		router.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(authentication.WithPrincipal(c.Request.Context(), value))
			c.Next()
		})
	}
	router.Use(NewAuthorizationChecker(authorizer).RequirePermission("documents", "write"))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	return recorder
}

func TestAuthorizationStatusMapping(t *testing.T) {
	t.Parallel()

	allow := authorizerFunc(func(_ context.Context, request authorization.Request) (authorization.Decision, error) {
		if request.Resource != "documents" || request.Action != "write" || request.Principal.Subject != "user:42" {
			t.Fatalf("unexpected request: %+v", request)
		}
		return authorization.Decision{Allowed: true, Reason: authorization.ReasonAllowed, Revision: 7}, nil
	})

	if recorder := serveAuthorization(t, nil, allow); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing principal status = %d, want 401", recorder.Code)
	}
	anonymous := authentication.Anonymous()
	if recorder := serveAuthorization(t, &anonymous, allow); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous principal status = %d, want 401", recorder.Code)
	}
	principal := validPrincipal()
	if recorder := serveAuthorization(t, &principal, allow); recorder.Code != http.StatusNoContent {
		t.Fatalf("allow status = %d, want 204", recorder.Code)
	}

	deny := authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
		return authorization.Decision{Allowed: false, Reason: authorization.ReasonPolicyDenied}, nil
	})
	if recorder := serveAuthorization(t, &principal, deny); recorder.Code != http.StatusForbidden {
		t.Fatalf("deny status = %d, want 403", recorder.Code)
	}

	failure := authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
		return authorization.Decision{}, errors.New("casbin database password=secret")
	})
	recorder := serveAuthorization(t, &principal, failure)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("evaluator failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRequireAnyPermissionShortCircuitsOnFirstAllowedRequirement(t *testing.T) {
	t.Parallel()
	principal := validPrincipal()
	requests := make([]authorization.Request, 0, 2)
	authorizer := authorizerFunc(func(_ context.Context, request authorization.Request) (authorization.Decision, error) {
		requests = append(requests, request)
		return authorization.Decision{Allowed: request.Resource == "authorization"}, nil
	})

	recorder := serveAnyAuthorization(t, &principal, authorizer,
		PermissionRequirement{Resource: "role", Action: "read"},
		PermissionRequirement{Resource: "authorization", Action: "manage"},
		PermissionRequirement{Resource: "never", Action: "called"},
	)

	if recorder.Code != http.StatusNoContent || len(requests) != 2 {
		t.Fatalf("status=%d requests=%+v", recorder.Code, requests)
	}
}

func TestRequireAnyPermissionStopsAfterFirstRequirementAllows(t *testing.T) {
	t.Parallel()
	principal := validPrincipal()
	calls := 0
	authorizer := authorizerFunc(func(_ context.Context, request authorization.Request) (authorization.Decision, error) {
		calls++
		return authorization.Decision{Allowed: request.Resource == "role"}, nil
	})

	recorder := serveAnyAuthorization(t, &principal, authorizer,
		PermissionRequirement{Resource: "role", Action: "read"},
		PermissionRequirement{Resource: "authorization", Action: "manage"},
	)

	if recorder.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("status=%d calls=%d, want 204 and one evaluation", recorder.Code, calls)
	}
}

func TestRequireAnyPermissionFailsClosed(t *testing.T) {
	t.Parallel()
	principal := validPrincipal()
	deny := authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
		return authorization.Decision{Allowed: false}, nil
	})
	if recorder := serveAnyAuthorization(t, &principal, deny,
		PermissionRequirement{Resource: "role", Action: "read"},
		PermissionRequirement{Resource: "authorization", Action: "manage"},
	); recorder.Code != http.StatusForbidden {
		t.Fatalf("all denied status=%d, want 403", recorder.Code)
	}
	if recorder := serveAnyAuthorization(t, nil, deny,
		PermissionRequirement{Resource: "role", Action: "read"},
	); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing principal status=%d, want 401", recorder.Code)
	}
	if recorder := serveAnyAuthorization(t, &principal, deny); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("empty requirements status=%d, want 500", recorder.Code)
	}
	if recorder := serveAnyAuthorization(t, &principal, deny,
		PermissionRequirement{Resource: "", Action: "read"},
	); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("invalid requirement status=%d, want 500", recorder.Code)
	}
}

func TestRequireAnyPermissionDoesNotHideEvaluationFailure(t *testing.T) {
	t.Parallel()
	principal := validPrincipal()
	authorizer := authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
		return authorization.Decision{}, errors.New("policy runtime unavailable")
	})

	recorder := serveAnyAuthorization(t, &principal, authorizer,
		PermissionRequirement{Resource: "role", Action: "read"},
		PermissionRequirement{Resource: "authorization", Action: "manage"},
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("evaluation failure status=%d, want 500", recorder.Code)
	}
}

func serveAnyAuthorization(
	t *testing.T,
	principal *authentication.Principal,
	authorizer authorization.Authorizer,
	requirements ...PermissionRequirement,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if principal != nil {
		value := *principal
		router.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(authentication.WithPrincipal(c.Request.Context(), value))
			c.Next()
		})
	}
	router.Use(NewAuthorizationChecker(authorizer).RequireAnyPermission(requirements...))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	return recorder
}
