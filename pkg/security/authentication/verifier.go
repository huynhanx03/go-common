package authentication

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"math/big"

	"github.com/golang-jwt/jwt/v5"
)

type JWTVerifier struct {
	keys    verificationKeys
	options VerifierOptions
}

var _ Verifier = (*JWTVerifier)(nil)

// KeySet contains public keys accepted for token verification.
// Keep old keys here during a planned rotation until their tokens expire.
type KeySet struct {
	VerificationKeys map[string]*rsa.PublicKey
}

type verificationKeys map[string]*rsa.PublicKey

func (v *JWTVerifier) Verify(
	ctx context.Context,
	raw string,
	expected TokenPurpose,
) (Principal, error) {
	if v == nil || v.options.Clock == nil || ctx == nil {
		return Anonymous(), ErrInvalidToken
	}
	if err := ctx.Err(); err != nil {
		return Anonymous(), err
	}
	if err := expected.Validate(); err != nil {
		return Anonymous(), err
	}
	if raw == "" || len(raw) > v.options.MaxTokenBytes {
		return Anonymous(), ErrInvalidToken
	}

	claims := &Claims{}
	_, err := jwt.ParseWithClaims(
		raw,
		claims,
		v.keyForToken,
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(v.options.Issuer),
		jwt.WithAudience(v.options.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(v.options.Leeway),
		jwt.WithTimeFunc(v.options.Clock),
	)
	if err != nil {
		return Anonymous(), classifyParseError(err)
	}
	if err := ctx.Err(); err != nil {
		return Anonymous(), err
	}
	if claims.Purpose.Validate() != nil {
		return Anonymous(), ErrInvalidPurpose
	}
	if claims.Purpose != expected {
		return Anonymous(), ErrPurposeMismatch
	}
	if err := validateVerifiedClaims(claims); err != nil {
		return Anonymous(), err
	}

	principal := Principal{
		Authenticated:      true,
		Subject:            claims.Subject,
		UserID:             claims.UserID,
		SessionID:          claims.SessionID,
		Username:           claims.Username,
		TokenID:            claims.ID,
		SecurityVersion:    claims.SecurityVersion,
		AuthenticatedUntil: claims.ExpiresAt.Time.UTC(),
	}
	if claims.AuthTime != nil {
		principal.AuthenticatedAt = claims.AuthTime.Time.UTC()
	}
	now := v.options.Clock().UTC()
	if !principal.AuthenticatedUntil.After(now) {
		return Anonymous(), ErrExpiredToken
	}
	return principal, nil
}

func (v *JWTVerifier) keyForToken(token *jwt.Token) (any, error) {
	if token == nil || token.Method != jwt.SigningMethodRS256 {
		return nil, ErrInvalidToken
	}
	keyID, ok := token.Header[jwtKeyIDHeader].(string)
	if !ok || validateKeyID(keyID) != nil {
		return nil, ErrInvalidToken
	}
	if publicKey, exists := v.keys[keyID]; exists {
		return publicKey, nil
	}
	return nil, ErrUnknownKey
}

func validateVerifiedClaims(claims *Claims) error {
	fields := []struct {
		name     string
		value    string
		required bool
	}{
		{name: subjectFieldName, value: claims.Subject, required: true},
		{name: userIDFieldName, value: claims.UserID, required: true},
		{name: sessionIDFieldName, value: claims.SessionID, required: true},
		{name: usernameFieldName, value: claims.Username},
		{name: tokenIDFieldName, value: claims.ID, required: true},
	}
	for _, field := range fields {
		if err := validateTokenField(field.name, field.value, field.required, ErrInvalidToken); err != nil {
			return err
		}
	}
	if claims.ExpiresAt == nil ||
		claims.IssuedAt == nil ||
		!claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
		return ErrInvalidToken
	}
	if (claims.AuthTime == nil) != (claims.SecurityVersion == 0) {
		return ErrInvalidToken
	}
	if claims.AuthTime != nil && claims.AuthTime.Time.After(claims.IssuedAt.Time.Add(maxIssueClockSkew)) {
		return ErrInvalidToken
	}
	return nil
}

func classifyParseError(err error) error {
	switch {
	case errors.Is(err, ErrUnknownKey):
		return ErrUnknownKey
	case errors.Is(err, jwt.ErrTokenExpired):
		return ErrExpiredToken
	default:
		return ErrInvalidToken
	}
}

func copyKeySet(input KeySet) (verificationKeys, error) {
	if len(input.VerificationKeys) == 0 {
		return nil, fmt.Errorf("%w: no verification keys", ErrInvalidKeySet)
	}
	keys := make(verificationKeys, len(input.VerificationKeys))
	for keyID, publicKey := range input.VerificationKeys {
		if err := validateKeyID(keyID); err != nil {
			return nil, err
		}
		if err := validatePublicKey(publicKey); err != nil {
			return nil, err
		}
		keys[keyID] = &rsa.PublicKey{
			N: new(big.Int).Set(publicKey.N),
			E: publicKey.E,
		}
	}
	return keys, nil
}

func validatePublicKey(publicKey *rsa.PublicKey) error {
	if publicKey == nil || publicKey.N == nil || publicKey.N.BitLen() < minimumRSAKeyBits || publicKey.E < 3 {
		return fmt.Errorf("%w: RSA key must be at least %d bits", ErrInvalidKeySet, minimumRSAKeyBits)
	}
	return nil
}
