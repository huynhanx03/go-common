package authentication

import (
	"context"
	"fmt"
	"time"
)

type TokenPurpose string

const (
	PurposeAccess  TokenPurpose = "access"
	PurposeRefresh TokenPurpose = "refresh"
)

func (p TokenPurpose) Validate() error {
	switch p {
	case PurposeAccess, PurposeRefresh:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidPurpose, p)
	}
}

type Verifier interface {
	Verify(ctx context.Context, raw string, expected TokenPurpose) (Principal, error)
}

type IssueRequest struct {
	Purpose   TokenPurpose
	Subject   string
	UserID    string
	SessionID string
	Username  string
	TokenID   string
	// AuthenticatedAt and SecurityVersion are an optional pair for
	// applications that bind short-lived tokens to live session state.
	AuthenticatedAt time.Time
	SecurityVersion uint64
	IssuedAt        time.Time
	ExpiresAt       time.Time
}

type IssuedToken struct {
	Raw       string
	TokenID   string
	ExpiresAt time.Time
	KeyID     string
}
