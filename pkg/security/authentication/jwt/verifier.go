package jwt

import (
	"context"
	"errors"

	jwtlib "github.com/golang-jwt/jwt/v5"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type Verifier struct {
	keys    immutableKeySet
	options VerifierOptions
}

func NewVerifier(keys KeySet, options VerifierOptions) (*Verifier, error) {
	normalized, err := normalizeVerifierOptions(options)
	if err != nil {
		return nil, err
	}
	copied, err := copyKeySet(keys)
	if err != nil {
		return nil, err
	}
	return &Verifier{keys: copied, options: normalized}, nil
}

func (v *Verifier) Verify(
	ctx context.Context,
	raw string,
	expected authentication.TokenPurpose,
) (authentication.Principal, error) {
	if v == nil || v.options.Clock == nil || ctx == nil {
		return authentication.Anonymous(), authentication.ErrInvalidToken
	}
	if err := ctx.Err(); err != nil {
		return authentication.Anonymous(), err
	}
	if err := expected.Validate(); err != nil {
		return authentication.Anonymous(), err
	}
	if raw == "" || len(raw) > v.options.MaxTokenBytes {
		return authentication.Anonymous(), authentication.ErrInvalidToken
	}

	claims := &Claims{}
	token, err := jwtlib.ParseWithClaims(
		raw,
		claims,
		v.keyForToken,
		jwtlib.WithValidMethods([]string{jwtlib.SigningMethodRS256.Alg()}),
		jwtlib.WithIssuer(v.options.Issuer),
		jwtlib.WithAudience(v.options.Audience),
		jwtlib.WithExpirationRequired(),
		jwtlib.WithIssuedAt(),
		jwtlib.WithLeeway(v.options.Leeway),
		jwtlib.WithTimeFunc(v.options.Clock),
	)
	if err != nil {
		return authentication.Anonymous(), classifyParseError(err)
	}
	if err := ctx.Err(); err != nil {
		return authentication.Anonymous(), err
	}
	if token == nil || !token.Valid {
		return authentication.Anonymous(), authentication.ErrInvalidToken
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return authentication.Anonymous(), authentication.ErrInvalidToken
	}
	if claims.Purpose.Validate() != nil {
		return authentication.Anonymous(), authentication.ErrInvalidPurpose
	}
	if claims.Type != "" && claims.Type != claims.Purpose {
		return authentication.Anonymous(), authentication.ErrInvalidToken
	}
	if claims.Purpose != expected {
		return authentication.Anonymous(), authentication.ErrPurposeMismatch
	}
	if err := validateVerifiedClaims(claims); err != nil {
		return authentication.Anonymous(), err
	}

	principal := authentication.Principal{
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
		return authentication.Anonymous(), authentication.ErrExpiredToken
	}
	if err := principal.Validate(now); err != nil {
		return authentication.Anonymous(), authentication.ErrInvalidToken
	}
	return principal, nil
}

func (v *Verifier) keyForToken(token *jwtlib.Token) (any, error) {
	if token == nil || token.Method != jwtlib.SigningMethodRS256 {
		return nil, authentication.ErrInvalidToken
	}
	keyID, ok := token.Header["kid"].(string)
	if !ok || validateKeyID(keyID) != nil {
		return nil, authentication.ErrInvalidToken
	}
	if publicKey, exists := v.keys.active[keyID]; exists {
		return publicKey, nil
	}
	if _, retired := v.keys.retired[keyID]; retired {
		return nil, authentication.ErrRetiredKey
	}
	return nil, authentication.ErrUnknownKey
}

func validateVerifiedClaims(claims *Claims) error {
	if claims == nil {
		return authentication.ErrInvalidToken
	}
	fields := []struct {
		name     string
		value    string
		required bool
	}{
		{name: "subject", value: claims.Subject, required: true},
		{name: "user ID", value: claims.UserID, required: true},
		{name: "session ID", value: claims.SessionID, required: true},
		{name: "username", value: claims.Username},
		{name: "token ID", value: claims.ID, required: true},
	}
	for _, field := range fields {
		if err := validateClaim(field.name, field.value, field.required); err != nil {
			return err
		}
	}
	if claims.ExpiresAt == nil ||
		claims.IssuedAt == nil ||
		!claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
		return authentication.ErrInvalidToken
	}
	if (claims.AuthTime == nil) != (claims.SecurityVersion == 0) {
		return authentication.ErrInvalidToken
	}
	if claims.AuthTime != nil && claims.AuthTime.Time.After(claims.IssuedAt.Time.Add(maxIssueClockSkew)) {
		return authentication.ErrInvalidToken
	}
	return nil
}

func classifyParseError(err error) error {
	switch {
	case errors.Is(err, authentication.ErrRetiredKey):
		return authentication.ErrRetiredKey
	case errors.Is(err, authentication.ErrUnknownKey):
		return authentication.ErrUnknownKey
	case errors.Is(err, jwtlib.ErrTokenExpired):
		return authentication.ErrExpiredToken
	default:
		return authentication.ErrInvalidToken
	}
}

var _ authentication.Verifier = (*Verifier)(nil)
