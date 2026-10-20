package authentication_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func TestAnonymousContainsNoIdentity(t *testing.T) {
	principal := authentication.Anonymous()
	if principal.Authenticated ||
		principal.Subject != "" ||
		principal.UserID != "" ||
		principal.SessionID != "" ||
		principal.Username != "" ||
		principal.TokenID != "" ||
		!principal.AuthenticatedAt.IsZero() ||
		principal.SecurityVersion != 0 ||
		!principal.AuthenticatedUntil.IsZero() {
		t.Fatalf("Anonymous() = %#v", principal)
	}
	if err := principal.Validate(time.Now()); err != nil {
		t.Fatalf("Anonymous().Validate() error = %v", err)
	}
}

func TestPrincipalValidateEnforcesAuthenticatedInvariant(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	valid := authentication.Principal{
		Authenticated:      true,
		Subject:            "user:123",
		UserID:             "123",
		SessionID:          "session-1",
		TokenID:            "token-1",
		AuthenticatedUntil: now.Add(time.Minute),
	}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []authentication.Principal{
		{Authenticated: true, AuthenticatedUntil: now.Add(time.Minute)},
		{Authenticated: true, Subject: "user:123", AuthenticatedUntil: now},
		{Subject: "leaked-anonymous-identity"},
	}
	for _, principal := range tests {
		if err := principal.Validate(now); !errors.Is(err, authentication.ErrInvalidPrincipal) {
			t.Errorf("Validate(%#v) error = %v", principal, err)
		}
	}
}

func TestPrincipalRecentlyAuthenticatedUsesClosedWindow(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	principal := authentication.Principal{
		Authenticated:      true,
		Subject:            "user:123",
		AuthenticatedAt:    now.Add(-10 * time.Minute),
		AuthenticatedUntil: now.Add(time.Minute),
	}

	if !principal.RecentlyAuthenticated(now, 10*time.Minute) {
		t.Fatal("exact recent-authentication boundary was rejected")
	}
	for name, mutate := range map[string]func(*authentication.Principal){
		"stale": func(value *authentication.Principal) {
			value.AuthenticatedAt = now.Add(-10*time.Minute - time.Nanosecond)
		},
		"future": func(value *authentication.Principal) {
			value.AuthenticatedAt = now.Add(time.Nanosecond)
		},
		"missing": func(value *authentication.Principal) {
			value.AuthenticatedAt = time.Time{}
		},
		"expired principal": func(value *authentication.Principal) {
			value.AuthenticatedUntil = now
		},
		"anonymous": func(value *authentication.Principal) {
			*value = authentication.Anonymous()
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := principal
			mutate(&candidate)
			if candidate.RecentlyAuthenticated(now, 10*time.Minute) {
				t.Fatalf("RecentlyAuthenticated() accepted %#v", candidate)
			}
		})
	}
	if principal.RecentlyAuthenticated(now, 0) ||
		principal.RecentlyAuthenticated(time.Time{}, 10*time.Minute) {
		t.Fatal("invalid recent-authentication policy was accepted")
	}
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	principal := authentication.Principal{
		Authenticated:      true,
		Subject:            "user:123",
		AuthenticatedUntil: time.Now().Add(time.Minute),
	}
	ctx := authentication.WithPrincipal(context.Background(), principal)
	got, ok := authentication.FromContext(ctx)
	if !ok || got != principal {
		t.Fatalf("FromContext() = %#v, %t", got, ok)
	}
	if _, ok := authentication.FromContext(context.Background()); ok {
		t.Fatal("FromContext(empty) ok = true")
	}
	if _, ok := authentication.FromContext(nil); ok {
		t.Fatal("FromContext(nil) ok = true")
	}

	nilParent := authentication.WithPrincipal(nil, principal)
	if got, ok := authentication.FromContext(nilParent); !ok || got != principal {
		t.Fatalf("FromContext(WithPrincipal(nil)) = %#v, %t", got, ok)
	}
}

func TestTokenPurposeValidate(t *testing.T) {
	t.Parallel()

	for _, purpose := range []authentication.TokenPurpose{
		authentication.PurposeAccess,
		authentication.PurposeRefresh,
	} {
		if err := purpose.Validate(); err != nil {
			t.Errorf("%q.Validate() error = %v", purpose, err)
		}
	}
	if err := authentication.TokenPurpose("admin").Validate(); !errors.Is(
		err,
		authentication.ErrInvalidPurpose,
	) {
		t.Fatalf("unknown purpose error = %v", err)
	}
}
