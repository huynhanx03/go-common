package utils

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenType represents the type of token
type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"

	AccessTokenDuration  = 15 * time.Minute
	RefreshTokenDuration = 7 * 24 * time.Hour
)

// Claims extends standard jwt.Claims
type Claims struct {
	jwt.RegisteredClaims
	UserID   int       `json:"sub_int"`
	Username string    `json:"username"`
	Type     TokenType `json:"type"`
}

// GenerateToken generates a JWT token using RS256
func GenerateToken(
	privateKey *rsa.PrivateKey,
	userID int,
	username string,
	tokenType TokenType,
) (string, error) {
	duration := AccessTokenDuration
	if tokenType == RefreshToken {
		duration = RefreshTokenDuration
	}

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   username,
		},
		UserID:   userID,
		Username: username,
		Type:     tokenType,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(privateKey)
}

// ParseRSAPrivateKey parses a PEM encoded private key
func ParseRSAPrivateKey(key []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(key)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}

	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	}

	rsaKey, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not of type *rsa.PrivateKey")
	}
	return rsaKey, nil
}

// ParseRSAPublicKey parses a PEM encoded public key
func ParseRSAPublicKey(key []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(key)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// Try parsing as PKCS1 Public Key
		if rsaPub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
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
