package authentication

import (
	"context"
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Issuer struct {
	options    IssuerOptions
	privateKey *rsa.PrivateKey
}

func (i *Issuer) Issue(
	ctx context.Context,
	request IssueRequest,
) (IssuedToken, error) {
	if i == nil || i.privateKey == nil || i.options.Clock == nil || ctx == nil {
		return IssuedToken{}, ErrIssueRejected
	}
	if err := ctx.Err(); err != nil {
		return IssuedToken{}, err
	}
	if err := request.validate(i.options.Clock(), i.options.MaxTTL); err != nil {
		return IssuedToken{}, err
	}
	expiresAt := request.ExpiresAt.UTC()
	issuedAt := request.IssuedAt.UTC()

	tokenID := request.TokenID
	if tokenID == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return IssuedToken{}, fmt.Errorf(
				"%w: token ID generation failed",
				ErrIssueRejected,
			)
		}
		tokenID = generated.String()
	}
	if err := validateTokenField(tokenIDFieldName, tokenID, true, ErrIssueRejected); err != nil {
		return IssuedToken{}, err
	}

	var authTime *jwt.NumericDate
	if !request.AuthenticatedAt.IsZero() {
		authTime = jwt.NewNumericDate(request.AuthenticatedAt.UTC())
	}
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.options.Issuer,
			Subject:   request.Subject,
			Audience:  jwt.ClaimStrings{i.options.Audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(issuedAt),
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ID:        tokenID,
		},
		Purpose:         request.Purpose,
		UserID:          request.UserID,
		SessionID:       request.SessionID,
		Username:        request.Username,
		AuthTime:        authTime,
		SecurityVersion: request.SecurityVersion,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header[jwtKeyIDHeader] = i.options.KeyID
	raw, err := token.SignedString(i.privateKey)
	if err != nil {
		return IssuedToken{}, fmt.Errorf(
			"%w: signing failed",
			ErrIssueRejected,
		)
	}
	if err := ctx.Err(); err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{
		Raw:       raw,
		TokenID:   tokenID,
		ExpiresAt: expiresAt,
		KeyID:     i.options.KeyID,
	}, nil
}

func (request IssueRequest) validate(now time.Time, maxTTL time.Duration) error {
	if err := request.Purpose.Validate(); err != nil {
		return fmt.Errorf("%w: invalid purpose", ErrIssueRejected)
	}
	for _, field := range []struct {
		name     string
		value    string
		required bool
	}{
		{name: subjectFieldName, value: request.Subject, required: true},
		{name: userIDFieldName, value: request.UserID, required: true},
		{name: sessionIDFieldName, value: request.SessionID, required: true},
		{name: usernameFieldName, value: request.Username},
	} {
		if err := validateTokenField(field.name, field.value, field.required, ErrIssueRejected); err != nil {
			return err
		}
	}
	if request.IssuedAt.IsZero() || request.ExpiresAt.IsZero() ||
		!request.ExpiresAt.After(request.IssuedAt) ||
		request.IssuedAt.After(now.Add(maxIssueClockSkew)) ||
		request.ExpiresAt.Sub(request.IssuedAt) > maxTTL {
		return fmt.Errorf("%w: invalid token lifetime", ErrIssueRejected)
	}
	if request.AuthenticatedAt.IsZero() != (request.SecurityVersion == 0) {
		return fmt.Errorf("%w: authentication time and security version must be provided together", ErrIssueRejected)
	}
	if !request.AuthenticatedAt.IsZero() && request.AuthenticatedAt.After(request.IssuedAt.Add(maxIssueClockSkew)) {
		return fmt.Errorf("%w: authentication time is after issue time", ErrIssueRejected)
	}
	return nil
}
