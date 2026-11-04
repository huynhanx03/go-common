package authentication

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
)

type TokenPurpose string

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

// Claims are the JWT claims issued and accepted by this package.
type Claims struct {
	jwt.RegisteredClaims
	Purpose         TokenPurpose     `json:"purpose"`
	UserID          string           `json:"user_id"`
	SessionID       string           `json:"session_id"`
	Username        string           `json:"username,omitempty"`
	AuthTime        *jwt.NumericDate `json:"auth_time,omitempty"`
	SecurityVersion uint64           `json:"security_version,omitempty"`
}

func validateTokenField(name, value string, required bool, rejection error) error {
	if value == "" {
		if required {
			return fmt.Errorf("%w: %s is required", rejection, name)
		}
		return nil
	}
	if len(value) > maxClaimBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s is invalid", rejection, name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s is invalid", rejection, name)
		}
	}
	return nil
}
