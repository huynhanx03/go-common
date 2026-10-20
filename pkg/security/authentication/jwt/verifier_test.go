package jwt_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	authjwt "github.com/huynhanx03/go-common/pkg/security/authentication/jwt"
)

func TestIssuerVerifierRoundTripAndPurposeBoundary(t *testing.T) {
	fixture := newFixture(t)
	issued, err := fixture.issuer.Issue(context.Background(), authentication.IssueRequest{
		Purpose:         authentication.PurposeAccess,
		Subject:         "user:42",
		UserID:          "42",
		SessionID:       "session-7",
		Username:        "jerry",
		TokenID:         "token-9",
		AuthenticatedAt: fixture.now.Add(-5 * time.Minute),
		SecurityVersion: 7,
		IssuedAt:        fixture.now,
		ExpiresAt:       fixture.now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Raw == "" || issued.KeyID != "active-1" || issued.TokenID != "token-9" {
		t.Fatalf("issued = %#v", issued)
	}

	principal, err := fixture.verifier.Verify(
		context.Background(),
		issued.Raw,
		authentication.PurposeAccess,
	)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !principal.Authenticated ||
		principal.Subject != "user:42" ||
		principal.UserID != "42" ||
		principal.SessionID != "session-7" ||
		principal.Username != "jerry" ||
		principal.TokenID != "token-9" ||
		!principal.AuthenticatedAt.Equal(fixture.now.Add(-5*time.Minute)) ||
		principal.SecurityVersion != 7 ||
		!principal.AuthenticatedUntil.Equal(fixture.now.Add(15*time.Minute)) {
		t.Fatalf("principal = %#v", principal)
	}

	principal, err = fixture.verifier.Verify(
		context.Background(),
		issued.Raw,
		authentication.PurposeRefresh,
	)
	if !errors.Is(err, authentication.ErrPurposeMismatch) || principal.Authenticated {
		t.Fatalf("Verify(cross-purpose) = %#v, %v", principal, err)
	}
}

func TestVerifierRequiresIssuerAudiencePurposeAndKid(t *testing.T) {
	fixture := newFixture(t)
	base := authjwt.Claims{
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    "example-service",
			Subject:   "user:42",
			Audience:  jwtlib.ClaimStrings{"example-client"},
			ExpiresAt: jwtlib.NewNumericDate(fixture.now.Add(time.Minute)),
			IssuedAt:  jwtlib.NewNumericDate(fixture.now),
			ID:        "token-1",
		},
		Purpose:   authentication.PurposeAccess,
		UserID:    "42",
		SessionID: "session-1",
	}

	tests := []struct {
		name       string
		mutate     func(*authjwt.Claims)
		includeKID bool
	}{
		{name: "missing issuer", mutate: func(claims *authjwt.Claims) { claims.Issuer = "" }, includeKID: true},
		{name: "missing audience", mutate: func(claims *authjwt.Claims) { claims.Audience = nil }, includeKID: true},
		{name: "missing purpose", mutate: func(claims *authjwt.Claims) { claims.Purpose = "" }, includeKID: true},
		{name: "missing issued at", mutate: func(claims *authjwt.Claims) { claims.IssuedAt = nil }, includeKID: true},
		{name: "missing expiry", mutate: func(claims *authjwt.Claims) { claims.ExpiresAt = nil }, includeKID: true},
		{name: "missing token id", mutate: func(claims *authjwt.Claims) { claims.ID = "" }, includeKID: true},
		{name: "missing session", mutate: func(claims *authjwt.Claims) { claims.SessionID = "" }, includeKID: true},
		{name: "security version without auth time", mutate: func(claims *authjwt.Claims) { claims.SecurityVersion = 1 }, includeKID: true},
		{name: "auth time without security version", mutate: func(claims *authjwt.Claims) { claims.AuthTime = jwtlib.NewNumericDate(fixture.now) }, includeKID: true},
		{name: "missing kid", mutate: func(*authjwt.Claims) {}, includeKID: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := base
			test.mutate(&claims)
			raw := signToken(t, fixture.privateKey, claims, test.includeKID, jwtlib.SigningMethodRS256)
			principal, err := fixture.verifier.Verify(
				context.Background(),
				raw,
				authentication.PurposeAccess,
			)
			if err == nil || principal.Authenticated {
				t.Fatalf("Verify() = %#v, %v", principal, err)
			}
		})
	}
}

func TestVerifierRejectsWrongAlgorithmUnknownAndRetiredKeys(t *testing.T) {
	fixture := newFixture(t)
	claims := fixture.validClaims()

	hsToken := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	hsToken.Header["kid"] = "active-1"
	hsRaw, err := hsToken.SignedString([]byte("not-rsa"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.verifier.Verify(context.Background(), hsRaw, authentication.PurposeAccess); !errors.Is(err, authentication.ErrInvalidToken) {
		t.Fatalf("Verify(HS256) error = %v", err)
	}

	unknown := signTokenWithKID(t, fixture.privateKey, claims, "unknown")
	if _, err := fixture.verifier.Verify(context.Background(), unknown, authentication.PurposeAccess); !errors.Is(err, authentication.ErrUnknownKey) {
		t.Fatalf("Verify(unknown kid) error = %v", err)
	}

	retiredPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := authjwt.NewVerifier(
		authjwt.KeySet{
			Active:  map[string]*rsa.PublicKey{"active-1": &fixture.privateKey.PublicKey},
			Retired: map[string]*rsa.PublicKey{"retired-1": &retiredPrivate.PublicKey},
		},
		fixture.verifierOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	retired := signTokenWithKID(t, retiredPrivate, claims, "retired-1")
	if _, err := verifier.Verify(context.Background(), retired, authentication.PurposeAccess); !errors.Is(err, authentication.ErrRetiredKey) {
		t.Fatalf("Verify(retired kid) error = %v", err)
	}
}

func TestVerifierCopiesKeyMaps(t *testing.T) {
	fixture := newFixture(t)
	active := map[string]*rsa.PublicKey{"active-1": &fixture.privateKey.PublicKey}
	verifier, err := authjwt.NewVerifier(
		authjwt.KeySet{Active: active},
		fixture.verifierOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	delete(active, "active-1")

	raw := signTokenWithKID(t, fixture.privateKey, fixture.validClaims(), "active-1")
	if _, err := verifier.Verify(context.Background(), raw, authentication.PurposeAccess); err != nil {
		t.Fatalf("Verify() after caller map mutation error = %v", err)
	}
}

func TestVerifierUsesBoundedClockSkewAndRedactsRawToken(t *testing.T) {
	fixture := newFixture(t)
	claims := fixture.validClaims()
	claims.IssuedAt = jwtlib.NewNumericDate(fixture.now.Add(30 * time.Second))
	raw := signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	if _, err := fixture.verifier.Verify(context.Background(), raw, authentication.PurposeAccess); err != nil {
		t.Fatalf("Verify(within leeway) error = %v", err)
	}

	claims.IssuedAt = jwtlib.NewNumericDate(fixture.now.Add(2 * time.Minute))
	raw = signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	principal, err := fixture.verifier.Verify(context.Background(), raw, authentication.PurposeAccess)
	if !errors.Is(err, authentication.ErrInvalidToken) || principal.Authenticated {
		t.Fatalf("Verify(future issued-at) = %#v, %v", principal, err)
	}

	claims.IssuedAt = jwtlib.NewNumericDate(fixture.now.Add(-3 * time.Minute))
	claims.ExpiresAt = jwtlib.NewNumericDate(fixture.now.Add(-2 * time.Minute))
	raw = signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	principal, err = fixture.verifier.Verify(context.Background(), raw, authentication.PurposeAccess)
	if !errors.Is(err, authentication.ErrExpiredToken) || principal.Authenticated {
		t.Fatalf("Verify(expired) = %#v, %v", principal, err)
	}
	if strings.Contains(err.Error(), raw) {
		t.Fatal("verification error contains raw token")
	}
}

func TestIssuerRejectsInvalidTimeAndMaximumTTL(t *testing.T) {
	fixture := newFixture(t)
	request := authentication.IssueRequest{
		Purpose:   authentication.PurposeAccess,
		Subject:   "user:42",
		UserID:    "42",
		SessionID: "session-1",
		IssuedAt:  fixture.now,
		ExpiresAt: fixture.now.Add(2 * time.Hour),
	}
	if _, err := fixture.issuer.Issue(context.Background(), request); !errors.Is(err, authentication.ErrIssueRejected) {
		t.Fatalf("Issue(overlong) error = %v", err)
	}

	request.ExpiresAt = fixture.now
	if _, err := fixture.issuer.Issue(context.Background(), request); !errors.Is(err, authentication.ErrIssueRejected) {
		t.Fatalf("Issue(invalid interval) error = %v", err)
	}
}

type fixture struct {
	now        time.Time
	privateKey *rsa.PrivateKey
	issuer     *authjwt.Issuer
	verifier   *authjwt.Verifier
}

var (
	testKeyOnce sync.Once
	testKey     *rsa.PrivateKey
	testKeyErr  error
)

func newFixture(t testing.TB) fixture {
	t.Helper()
	testKeyOnce.Do(func() {
		testKey, testKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if testKeyErr != nil {
		t.Fatal(testKeyErr)
	}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	result := fixture{now: now, privateKey: testKey}
	var err error
	result.issuer, err = authjwt.NewIssuer(authjwt.IssuerOptions{
		Issuer:     "example-service",
		Audience:   "example-client",
		KeyID:      "active-1",
		PrivateKey: testKey,
		MaxTTL:     time.Hour,
		Clock:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result.verifier, err = authjwt.NewVerifier(
		authjwt.KeySet{Active: map[string]*rsa.PublicKey{"active-1": &testKey.PublicKey}},
		result.verifierOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f fixture) verifierOptions() authjwt.VerifierOptions {
	return authjwt.VerifierOptions{
		Issuer:        "example-service",
		Audience:      "example-client",
		Leeway:        time.Minute,
		MaxTokenBytes: 16 << 10,
		Clock:         func() time.Time { return f.now },
	}
}

func (f fixture) validClaims() authjwt.Claims {
	return authjwt.Claims{
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    "example-service",
			Subject:   "user:42",
			Audience:  jwtlib.ClaimStrings{"example-client"},
			ExpiresAt: jwtlib.NewNumericDate(f.now.Add(time.Minute)),
			IssuedAt:  jwtlib.NewNumericDate(f.now),
			ID:        "token-1",
		},
		Purpose:   authentication.PurposeAccess,
		UserID:    "42",
		SessionID: "session-1",
	}
}

func signToken(
	t *testing.T,
	privateKey *rsa.PrivateKey,
	claims authjwt.Claims,
	includeKID bool,
	method jwtlib.SigningMethod,
) string {
	t.Helper()
	token := jwtlib.NewWithClaims(method, claims)
	if includeKID {
		token.Header["kid"] = "active-1"
	}
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signTokenWithKID(
	t *testing.T,
	privateKey *rsa.PrivateKey,
	claims authjwt.Claims,
	keyID string,
) string {
	t.Helper()
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, claims)
	token.Header["kid"] = keyID
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
