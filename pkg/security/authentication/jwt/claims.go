package jwt

import (
	jwtlib "github.com/golang-jwt/jwt/v5"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type Claims struct {
	jwtlib.RegisteredClaims
	Purpose         authentication.TokenPurpose `json:"purpose"`
	Type            authentication.TokenPurpose `json:"type,omitempty"`
	UserID          string                      `json:"user_id"`
	SessionID       string                      `json:"session_id"`
	Username        string                      `json:"username,omitempty"`
	AuthTime        *jwtlib.NumericDate         `json:"auth_time,omitempty"`
	SecurityVersion uint64                      `json:"security_version,omitempty"`
}
