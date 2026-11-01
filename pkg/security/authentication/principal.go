// Package authentication provides authenticated principals and RS256 JWT
// issuance and verification.
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
	// AuthenticatedAt is the token's asserted primary or step-up authentication time.
	AuthenticatedAt time.Time
	// SecurityVersion is the application's session/account version; zero disables it.
	SecurityVersion    uint64
	AuthenticatedUntil time.Time
}

func Anonymous() Principal {
	return Principal{}
}

func (p Principal) Validate(now time.Time) error {
	if p.Authenticated {
		if p.Subject == "" {
			return fmt.Errorf("%w: authenticated subject is empty", ErrInvalidPrincipal)
		}
		if p.AuthenticatedUntil.IsZero() || !p.AuthenticatedUntil.After(now) {
			return fmt.Errorf("%w: authentication boundary is not in the future", ErrInvalidPrincipal)
		}
		return nil
	}
	if p.Subject != "" || p.UserID != "" || p.SessionID != "" ||
		p.Username != "" || p.TokenID != "" ||
		!p.AuthenticatedAt.IsZero() || p.SecurityVersion != 0 ||
		!p.AuthenticatedUntil.IsZero() {
		return fmt.Errorf("%w: anonymous principal contains identity", ErrInvalidPrincipal)
	}
	return nil
}

// RecentlyAuthenticated checks that the asserted authentication age is within
// [0, maximumAge]. Applications must check live session state separately.
func (p Principal) RecentlyAuthenticated(now time.Time, maximumAge time.Duration) bool {
	if now.IsZero() || maximumAge <= 0 || p.Validate(now) != nil ||
		!p.Authenticated || p.AuthenticatedAt.IsZero() {
		return false
	}
	age := now.Sub(p.AuthenticatedAt)
	return age >= 0 && age <= maximumAge
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
