// Package authentication defines vendor-neutral authenticated principals and
// token issue/verification contracts.
package authentication

import (
	"context"
	"fmt"
	"time"
)

type Principal struct {
	Authenticated bool
	Subject       string
	UserID        string
	SessionID     string
	Username      string
	TokenID       string
	// AuthenticatedAt is the last primary or step-up authentication time
	// asserted by the verified credential. Session-aware applications must
	// still re-check their canonical server-side session state.
	AuthenticatedAt time.Time
	// SecurityVersion is an application-owned monotonic session/account
	// version. Zero means the issuing application does not use version fences.
	SecurityVersion    uint64
	AuthenticatedUntil time.Time
}

func Anonymous() Principal {
	return Principal{}
}

func (p Principal) Validate(now time.Time) error {
	if !p.Authenticated {
		if p.Subject != "" ||
			p.UserID != "" ||
			p.SessionID != "" ||
			p.Username != "" ||
			p.TokenID != "" ||
			!p.AuthenticatedAt.IsZero() ||
			p.SecurityVersion != 0 ||
			!p.AuthenticatedUntil.IsZero() {
			return fmt.Errorf("%w: anonymous principal contains identity", ErrInvalidPrincipal)
		}
		return nil
	}
	if p.Subject == "" {
		return fmt.Errorf("%w: authenticated subject is empty", ErrInvalidPrincipal)
	}
	if p.AuthenticatedUntil.IsZero() || !p.AuthenticatedUntil.After(now) {
		return fmt.Errorf("%w: authentication boundary is not in the future", ErrInvalidPrincipal)
	}
	return nil
}

// RecentlyAuthenticated reports whether an otherwise valid authenticated
// principal carries a primary/step-up authentication time within maximumAge.
// It rejects future assertions instead of hiding clock or issuer defects.
//
// This is a credential-level primitive. Session-aware applications must call
// it only after reconciling the principal with their canonical live session;
// a token assertion alone cannot prove that a session was not revoked.
func (p Principal) RecentlyAuthenticated(now time.Time, maximumAge time.Duration) bool {
	if now.IsZero() || maximumAge <= 0 || p.Validate(now) != nil ||
		!p.Authenticated || p.AuthenticatedAt.IsZero() {
		return false
	}
	now = now.UTC()
	authenticatedAt := p.AuthenticatedAt.UTC()
	if authenticatedAt.After(now) {
		return false
	}
	return now.Sub(authenticatedAt) <= maximumAge
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func FromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
