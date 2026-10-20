package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type verifierFunc func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error)

func (fn verifierFunc) Verify(ctx context.Context, raw string, purpose authentication.TokenPurpose) (authentication.Principal, error) {
	return fn(ctx, raw, purpose)
}

func validPrincipal() authentication.Principal {
	return authentication.Principal{
		Authenticated:      true,
		Subject:            "user:42",
		UserID:             "42",
		SessionID:          "session-42",
		Username:           "tester",
		TokenID:            "token-42",
		AuthenticatedUntil: time.Now().Add(time.Hour),
	}
}

func serveAuthentication(
	t *testing.T,
	middleware gin.HandlerFunc,
	configure func(*http.Request),
) (*httptest.ResponseRecorder, authentication.Principal, bool, context.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var principal authentication.Principal
	var present bool
	var handlerContext context.Context
	router := gin.New()
	router.Use(middleware)
	router.GET("/", func(c *gin.Context) {
		principal, present = authentication.FromContext(c.Request.Context())
		handlerContext = c.Request.Context()
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if configure != nil {
		configure(request)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder, principal, present, handlerContext
}

func TestAuthenticationWithVerifierRequired(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	expected := validPrincipal()
	verifier := verifierFunc(func(_ context.Context, raw string, purpose authentication.TokenPurpose) (authentication.Principal, error) {
		calls.Add(1)
		if purpose != authentication.PurposeAccess {
			t.Fatalf("purpose = %q, want access", purpose)
		}
		if raw != "valid-token" {
			return authentication.Anonymous(), authentication.ErrInvalidToken
		}
		return expected, nil
	})

	middleware := AuthenticationWithVerifier(verifier)
	recorder, _, present, _ := serveAuthentication(t, middleware, nil)
	if recorder.Code != http.StatusUnauthorized || present {
		t.Fatalf("missing credential status=%d present=%v", recorder.Code, present)
	}
	if calls.Load() != 0 {
		t.Fatal("verifier called for missing credential")
	}

	recorder, principal, present, _ := serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer valid-token")
	})
	if recorder.Code != http.StatusNoContent || !present || principal != expected {
		t.Fatalf("valid credential status=%d principal=%+v present=%v", recorder.Code, principal, present)
	}

	recorder, _, _, _ = serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Basic valid-token")
	})
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("malformed scheme status = %d, want 401", recorder.Code)
	}
}

func TestAuthenticationWithVerifierUsesExpectedTokenPurpose(t *testing.T) {
	t.Parallel()

	var received authentication.TokenPurpose
	verifier := verifierFunc(func(_ context.Context, _ string, purpose authentication.TokenPurpose) (authentication.Principal, error) {
		received = purpose
		return validPrincipal(), nil
	})
	middleware := AuthenticationWithVerifier(
		verifier,
		WithExpectedTokenPurpose(authentication.PurposeRefresh),
	)

	recorder, _, present, _ := serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer refresh-token")
	})
	if recorder.Code != http.StatusNoContent || !present {
		t.Fatalf("refresh credential status=%d present=%v", recorder.Code, present)
	}
	if received != authentication.PurposeRefresh {
		t.Fatalf("purpose = %q, want refresh", received)
	}
}

func TestAuthenticationRejectsInvalidExpectedTokenPurpose(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	middleware := AuthenticationWithVerifier(
		verifierFunc(func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
			calls.Add(1)
			return validPrincipal(), nil
		}),
		WithExpectedTokenPurpose(authentication.TokenPurpose("unknown")),
	)
	recorder, _, present, _ := serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer token")
	})
	if recorder.Code != http.StatusInternalServerError || present || calls.Load() != 0 {
		t.Fatalf("invalid purpose status=%d present=%v verifier calls=%d", recorder.Code, present, calls.Load())
	}
}

func TestOptionalAuthenticationOnlyAnonymousWhenAbsent(t *testing.T) {
	t.Parallel()

	verifier := verifierFunc(func(_ context.Context, raw string, _ authentication.TokenPurpose) (authentication.Principal, error) {
		if raw == "bad" {
			return authentication.Anonymous(), authentication.ErrPurposeMismatch
		}
		return validPrincipal(), nil
	})
	middleware := OptionalAuthenticationWithVerifier(verifier)

	recorder, principal, present, _ := serveAuthentication(t, middleware, nil)
	if recorder.Code != http.StatusNoContent || !present || principal != authentication.Anonymous() {
		t.Fatalf("absent optional status=%d principal=%+v present=%v", recorder.Code, principal, present)
	}

	recorder, _, _, _ = serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer bad")
	})
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("present-invalid optional status = %d, want 401", recorder.Code)
	}
}

func TestAuthenticationRejectsConflictingAndOversizeCredentials(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	verifier := verifierFunc(func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
		calls.Add(1)
		return validPrincipal(), nil
	})
	middleware := AuthenticationWithVerifier(
		verifier,
		WithCredentialSources(CredentialBearer, CredentialCookie),
		WithCredentialCookie("access_token"),
		WithMaxCredentialBytes(32),
	)

	recorder, _, _, _ := serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer bearer-token")
		request.AddCookie(&http.Cookie{Name: "access_token", Value: "cookie-token"})
	})
	if recorder.Code != http.StatusUnauthorized || calls.Load() != 0 {
		t.Fatalf("conflicting credentials status=%d verifier calls=%d", recorder.Code, calls.Load())
	}

	recorder, _, _, _ = serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 33))
	})
	if recorder.Code != http.StatusUnauthorized || calls.Load() != 0 {
		t.Fatalf("oversize credential status=%d verifier calls=%d", recorder.Code, calls.Load())
	}
}

func TestAuthenticationLegacyContextIsExplicit(t *testing.T) {
	t.Parallel()

	verifier := verifierFunc(func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
		return validPrincipal(), nil
	})
	requestWithToken := func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer token")
	}

	_, _, _, typedOnly := serveAuthentication(t, AuthenticationWithVerifier(verifier), requestWithToken)
	if got := typedOnly.Value(constraints.ContextKeyUserID); got != nil {
		t.Fatalf("typed middleware populated legacy user ID: %v", got)
	}

	_, _, _, compatible := serveAuthentication(t, AuthenticationWithVerifier(
		verifier,
		WithLegacyAuthenticationContext(),
	), requestWithToken)
	if got := compatible.Value(constraints.ContextKeyUserID); got != "42" {
		t.Fatalf("legacy user ID = %v, want 42", got)
	}
	if got := compatible.Value(constraints.ContextKeyUsername); got != "tester" {
		t.Fatalf("legacy username = %v, want tester", got)
	}
}

func TestAuthenticationVerifierFailureAndCancellation(t *testing.T) {
	t.Parallel()

	middleware := AuthenticationWithVerifier(verifierFunc(func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error) {
		return authentication.Anonymous(), errors.New("token=secret")
	}))
	recorder, _, _, _ := serveAuthentication(t, middleware, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer opaque")
	})
	if recorder.Code != http.StatusUnauthorized || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("verifier failure response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
