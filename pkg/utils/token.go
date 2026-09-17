package utils

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	authenticationjwt "github.com/huynhanx03/go-common/pkg/security/authentication/jwt"
)

const (
	legacyJWTIssuer   = "go-common-legacy"
	legacyJWTAudience = "go-common-legacy-consumer"
	legacyJWTKeyID    = "legacy-rs256"
)

// TokenType represents the type of token.
//
// Deprecated: use authentication.TokenPurpose.
type TokenType string

const (
	// Deprecated: use authentication.PurposeAccess.
	AccessToken TokenType = "access"
	// Deprecated: use authentication.PurposeRefresh.
	RefreshToken TokenType = "refresh"

	AccessTokenDuration  = 15 * time.Minute
	RefreshTokenDuration = 7 * 24 * time.Hour
)

// Claims extends standard JWT claims.
//
// Deprecated: use authenticationjwt.Claims and authentication.Principal.
type Claims struct {
	jwt.RegisteredClaims
	UserID   string    `json:"user_id"`
	Username string    `json:"username"`
	Type     TokenType `json:"type"`
}

// GenerateToken generates a JWT token using the bounded RS256 issuer.
//
// Deprecated: construct an authenticationjwt.Issuer once and call Issue.
func GenerateToken(
	privateKey *rsa.PrivateKey,
	userID string,
	username string,
	tokenType TokenType,
) (string, error) {
	var purpose authentication.TokenPurpose
	var duration time.Duration
	switch tokenType {
	case AccessToken:
		purpose = authentication.PurposeAccess
		duration = AccessTokenDuration
	case RefreshToken:
		purpose = authentication.PurposeRefresh
		duration = RefreshTokenDuration
	default:
		return "", fmt.Errorf(
			"%w: unsupported legacy token type",
			authentication.ErrIssueRejected,
		)
	}

	issuer, err := authenticationjwt.NewIssuer(authenticationjwt.IssuerOptions{
		Issuer:     legacyJWTIssuer,
		Audience:   legacyJWTAudience,
		KeyID:      legacyJWTKeyID,
		PrivateKey: privateKey,
		MaxTTL:     RefreshTokenDuration,
	})
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	issued, err := issuer.Issue(context.Background(), authentication.IssueRequest{
		Purpose:   purpose,
		Subject:   username,
		UserID:    userID,
		SessionID: "legacy:" + userID,
		Username:  username,
		IssuedAt:  now,
		ExpiresAt: now.Add(duration),
	})
	if err != nil {
		return "", err
	}
	return issued.Raw, nil
}

// ParseRSAPrivateKey parses a PEM encoded private key.
//
// Deprecated: keep key loading at the application composition root and pass
// typed *rsa.PrivateKey values to authenticationjwt.NewIssuer.
func ParseRSAPrivateKey(key []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(key)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}

	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1.
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	}

	rsaKey, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not of type *rsa.PrivateKey")
	}
	return rsaKey, nil
}

// ParseRSAPublicKey parses a PEM encoded public key.
//
// Deprecated: keep key loading at the application composition root and pass
// typed *rsa.PublicKey values to authenticationjwt.NewVerifier.
func ParseRSAPublicKey(key []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(key)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// Try parsing as PKCS1 Public Key.
		if rsaPub, parseErr := x509.ParsePKCS1PublicKey(block.Bytes); parseErr == nil {
			return rsaPub, nil
		}
		return nil, err
	}

	switch pub := pub.(type) {
	case *rsa.PublicKey:
		return pub, nil
	default:
		return nil, errors.New("key is not of type *rsa.PublicKey")
	}
}
