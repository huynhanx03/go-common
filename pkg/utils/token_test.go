package utils

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func TestGenerateTokenCompatibilityFacade(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := GenerateToken(privateKey, "user-1", "alice", AccessToken)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	claims := &struct {
		Claims
		Purpose   authentication.TokenPurpose `json:"purpose"`
		SessionID string                      `json:"session_id"`
	}{}
	token, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Header["kid"] != legacyJWTKeyID {
				t.Errorf("kid = %v, want %q", token.Header["kid"], legacyJWTKeyID)
			}
			return &privateKey.PublicKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(legacyJWTIssuer),
		jwt.WithAudience(legacyJWTAudience),
	)
	if err != nil {
		t.Fatalf("parse generated token: %v", err)
	}
	if !token.Valid {
		t.Fatal("generated token is not valid")
	}
	if claims.UserID != "user-1" ||
		claims.Username != "alice" ||
		claims.Type != AccessToken ||
		claims.Purpose != authentication.PurposeAccess ||
		claims.SessionID == "" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestGenerateTokenRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		private   *rsa.PrivateKey
		userID    string
		username  string
		tokenType TokenType
	}{
		{name: "nil key", userID: "user-1", username: "alice", tokenType: AccessToken},
		{name: "empty user", private: privateKey, username: "alice", tokenType: AccessToken},
		{name: "empty username", private: privateKey, userID: "user-1", tokenType: AccessToken},
		{name: "unknown type", private: privateKey, userID: "user-1", username: "alice", tokenType: "other"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, issueErr := GenerateToken(
				test.private,
				test.userID,
				test.username,
				test.tokenType,
			)
			if !errors.Is(issueErr, authentication.ErrIssueRejected) &&
				!errors.Is(issueErr, authentication.ErrInvalidConfiguration) {
				t.Fatalf("GenerateToken() error = %v, want typed rejection", issueErr)
			}
		})
	}
}
