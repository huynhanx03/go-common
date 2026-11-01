package authentication

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAnonymousContainsNoIdentity(t *testing.T) {
	principal := Anonymous()
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
	valid := Principal{
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

	tests := []Principal{
		{Authenticated: true, AuthenticatedUntil: now.Add(time.Minute)},
		{Authenticated: true, Subject: "user:123", AuthenticatedUntil: now},
		{Subject: "leaked-anonymous-identity"},
	}
	for _, principal := range tests {
		if err := principal.Validate(now); !errors.Is(err, ErrInvalidPrincipal) {
			t.Errorf("Validate(%#v) error = %v", principal, err)
		}
	}
}

func TestPrincipalRecentlyAuthenticatedUsesClosedWindow(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	principal := Principal{
		Authenticated:      true,
		Subject:            "user:123",
		AuthenticatedAt:    now.Add(-10 * time.Minute),
		AuthenticatedUntil: now.Add(time.Minute),
	}

	if !principal.RecentlyAuthenticated(now, 10*time.Minute) {
		t.Fatal("exact recent-authentication boundary was rejected")
	}
	if !principal.RecentlyAuthenticated(now.In(time.FixedZone("UTC+7", 7*60*60)), 10*time.Minute) {
		t.Fatal("the same instant in a different time zone was rejected")
	}
	for name, mutate := range map[string]func(*Principal){
		"stale": func(value *Principal) {
			value.AuthenticatedAt = now.Add(-10*time.Minute - time.Nanosecond)
		},
		"future": func(value *Principal) {
			value.AuthenticatedAt = now.Add(time.Nanosecond)
		},
		"missing": func(value *Principal) {
			value.AuthenticatedAt = time.Time{}
		},
		"expired principal": func(value *Principal) {
			value.AuthenticatedUntil = now
		},
		"anonymous": func(value *Principal) {
			*value = Anonymous()
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
	principal := Principal{
		Authenticated:      true,
		Subject:            "user:123",
		AuthenticatedUntil: time.Now().Add(time.Minute),
	}
	ctx := WithPrincipal(context.Background(), principal)
	got, ok := FromContext(ctx)
	if !ok || got != principal {
		t.Fatalf("FromContext() = %#v, %t", got, ok)
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("FromContext(empty) ok = true")
	}
	if _, ok := FromContext(nil); ok {
		t.Fatal("FromContext(nil) ok = true")
	}

	nilParent := WithPrincipal(nil, principal)
	if got, ok := FromContext(nilParent); !ok || got != principal {
		t.Fatalf("FromContext(WithPrincipal(nil)) = %#v, %t", got, ok)
	}
}
