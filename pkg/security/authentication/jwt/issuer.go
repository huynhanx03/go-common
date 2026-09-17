package jwt

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"strings"
	"time"
	"unicode"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

const (
	hardMaxTTL        = 30 * 24 * time.Hour
	maxIssueClockSkew = time.Minute
)

type Issuer struct {
	options    IssuerOptions
	privateKey *rsa.PrivateKey
}

func NewIssuer(options IssuerOptions) (*Issuer, error) {
	if err := validateIdentifier("issuer", options.Issuer); err != nil {
		return nil, err
	}
	if err := validateIdentifier("audience", options.Audience); err != nil {
		return nil, err
	}
	if err := validateKeyID(options.KeyID); err != nil {
		return nil, fmt.Errorf("%w: invalid signing key ID", authentication.ErrInvalidConfiguration)
	}
	if options.PrivateKey == nil ||
		options.PrivateKey.N == nil ||
		options.PrivateKey.N.BitLen() < 2048 {
		return nil, fmt.Errorf(
			"%w: RSA private key must be at least 2048 bits",
			authentication.ErrInvalidConfiguration,
		)
	}
	if err := options.PrivateKey.Validate(); err != nil {
		return nil, fmt.Errorf("%w: invalid RSA private key", authentication.ErrInvalidConfiguration)
	}
	if options.MaxTTL <= 0 || options.MaxTTL > hardMaxTTL {
		return nil, fmt.Errorf(
			"%w: maximum TTL is outside 0..%s",
			authentication.ErrInvalidConfiguration,
			hardMaxTTL,
		)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	privateKey, err := clonePrivateKey(options.PrivateKey)
	if err != nil {
		return nil, err
	}
	options.PrivateKey = nil
	return &Issuer{options: options, privateKey: privateKey}, nil
}

func (i *Issuer) Issue(
	ctx context.Context,
	request authentication.IssueRequest,
) (authentication.IssuedToken, error) {
	if i == nil || i.privateKey == nil || i.options.Clock == nil || ctx == nil {
		return authentication.IssuedToken{}, authentication.ErrIssueRejected
	}
	if err := ctx.Err(); err != nil {
		return authentication.IssuedToken{}, err
	}
	if err := request.Purpose.Validate(); err != nil {
		return authentication.IssuedToken{}, fmt.Errorf("%w: invalid purpose", authentication.ErrIssueRejected)
	}
	for _, field := range []struct {
		name     string
		value    string
		required bool
	}{
		{name: "subject", value: request.Subject, required: true},
		{name: "user ID", value: request.UserID, required: true},
		{name: "session ID", value: request.SessionID, required: true},
		{name: "username", value: request.Username},
	} {
		if err := validateIssueField(field.name, field.value, field.required); err != nil {
			return authentication.IssuedToken{}, err
		}
	}

	now := i.options.Clock().UTC()
	issuedAt := request.IssuedAt.UTC()
	expiresAt := request.ExpiresAt.UTC()
	if request.IssuedAt.IsZero() ||
		request.ExpiresAt.IsZero() ||
		!expiresAt.After(issuedAt) ||
		issuedAt.After(now.Add(maxIssueClockSkew)) ||
		expiresAt.Sub(issuedAt) > i.options.MaxTTL {
		return authentication.IssuedToken{}, fmt.Errorf(
			"%w: invalid token lifetime",
			authentication.ErrIssueRejected,
		)
	}
	if request.AuthenticatedAt.IsZero() != (request.SecurityVersion == 0) {
		return authentication.IssuedToken{}, fmt.Errorf(
			"%w: authentication time and security version must be provided together",
			authentication.ErrIssueRejected,
		)
	}
	if !request.AuthenticatedAt.IsZero() && request.AuthenticatedAt.UTC().After(issuedAt.Add(maxIssueClockSkew)) {
		return authentication.IssuedToken{}, fmt.Errorf(
			"%w: authentication time is after issue time",
			authentication.ErrIssueRejected,
		)
	}

	tokenID := request.TokenID
	if tokenID == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return authentication.IssuedToken{}, fmt.Errorf(
				"%w: token ID generation failed",
				authentication.ErrIssueRejected,
			)
		}
		tokenID = generated.String()
	}
	if err := validateIssueField("token ID", tokenID, true); err != nil {
		return authentication.IssuedToken{}, err
	}

	var authTime *jwtlib.NumericDate
	if !request.AuthenticatedAt.IsZero() {
		authTime = jwtlib.NewNumericDate(request.AuthenticatedAt.UTC())
	}
	claims := Claims{
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    i.options.Issuer,
			Subject:   request.Subject,
			Audience:  jwtlib.ClaimStrings{i.options.Audience},
			ExpiresAt: jwtlib.NewNumericDate(expiresAt),
			NotBefore: jwtlib.NewNumericDate(issuedAt),
			IssuedAt:  jwtlib.NewNumericDate(issuedAt),
			ID:        tokenID,
		},
		Purpose:         request.Purpose,
		Type:            request.Purpose,
		UserID:          request.UserID,
		SessionID:       request.SessionID,
		Username:        request.Username,
		AuthTime:        authTime,
		SecurityVersion: request.SecurityVersion,
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, claims)
	token.Header["kid"] = i.options.KeyID
	raw, err := token.SignedString(i.privateKey)
	if err != nil {
		return authentication.IssuedToken{}, fmt.Errorf(
			"%w: signing failed",
			authentication.ErrIssueRejected,
		)
	}
	if err := ctx.Err(); err != nil {
		return authentication.IssuedToken{}, err
	}
	return authentication.IssuedToken{
		Raw:       raw,
		TokenID:   tokenID,
		ExpiresAt: expiresAt,
		KeyID:     i.options.KeyID,
	}, nil
}

func validateIssueField(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%w: %s is required", authentication.ErrIssueRejected, name)
		}
		return nil
	}
	if len(value) > maxClaimBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s is invalid", authentication.ErrIssueRejected, name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s is invalid", authentication.ErrIssueRejected, name)
		}
	}
	return nil
}

func clonePrivateKey(privateKey *rsa.PrivateKey) (*rsa.PrivateKey, error) {
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: clone RSA private key",
			authentication.ErrInvalidConfiguration,
		)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(encoded)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: clone RSA private key",
			authentication.ErrInvalidConfiguration,
		)
	}
	cloned, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf(
			"%w: cloned key is not RSA",
			authentication.ErrInvalidConfiguration,
		)
	}
	return cloned, nil
}
