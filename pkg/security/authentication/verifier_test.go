package authentication

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIssuerVerifierRoundTripAndPurposeBoundary(t *testing.T) {
	fixture := newFixture(t)
	issued, err := fixture.issuer.Issue(context.Background(), IssueRequest{
		Purpose:         PurposeAccess,
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
		PurposeAccess,
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
		PurposeRefresh,
	)
	if !errors.Is(err, ErrPurposeMismatch) || principal.Authenticated {
		t.Fatalf("Verify(cross-purpose) = %#v, %v", principal, err)
	}
}

func TestVerifierRequiresIssuerAudiencePurposeAndKid(t *testing.T) {
	fixture := newFixture(t)
	base := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "example-service",
			Subject:   "user:42",
			Audience:  jwt.ClaimStrings{"example-client"},
			ExpiresAt: jwt.NewNumericDate(fixture.now.Add(time.Minute)),
			IssuedAt:  jwt.NewNumericDate(fixture.now),
			ID:        "token-1",
		},
		Purpose:   PurposeAccess,
		UserID:    "42",
		SessionID: "session-1",
	}

	tests := []struct {
		name       string
		mutate     func(*Claims)
		includeKID bool
	}{
		{name: "missing issuer", mutate: func(claims *Claims) { claims.Issuer = "" }, includeKID: true},
		{name: "missing audience", mutate: func(claims *Claims) { claims.Audience = nil }, includeKID: true},
		{name: "missing purpose", mutate: func(claims *Claims) { claims.Purpose = "" }, includeKID: true},
		{name: "missing subject", mutate: func(claims *Claims) { claims.Subject = "" }, includeKID: true},
		{name: "missing user", mutate: func(claims *Claims) { claims.UserID = "" }, includeKID: true},
		{name: "padded subject", mutate: func(claims *Claims) { claims.Subject = " user:42" }, includeKID: true},
		{name: "control character", mutate: func(claims *Claims) { claims.Username = "jerry\n" }, includeKID: true},
		{name: "missing issued at", mutate: func(claims *Claims) { claims.IssuedAt = nil }, includeKID: true},
		{name: "missing expiry", mutate: func(claims *Claims) { claims.ExpiresAt = nil }, includeKID: true},
		{name: "missing token id", mutate: func(claims *Claims) { claims.ID = "" }, includeKID: true},
		{name: "empty lifetime", mutate: func(claims *Claims) {
			claims.IssuedAt = jwt.NewNumericDate(fixture.now.Add(time.Minute))
			claims.ExpiresAt = jwt.NewNumericDate(fixture.now.Add(time.Minute))
		}, includeKID: true},
		{name: "missing session", mutate: func(claims *Claims) { claims.SessionID = "" }, includeKID: true},
		{name: "security version without auth time", mutate: func(claims *Claims) { claims.SecurityVersion = 1 }, includeKID: true},
		{name: "auth time without security version", mutate: func(claims *Claims) { claims.AuthTime = jwt.NewNumericDate(fixture.now) }, includeKID: true},
		{name: "future authentication", mutate: func(claims *Claims) {
			claims.AuthTime = jwt.NewNumericDate(fixture.now.Add(2 * time.Minute))
			claims.SecurityVersion = 1
		}, includeKID: true},
		{name: "missing kid", mutate: func(*Claims) {}, includeKID: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := base
			test.mutate(&claims)
			raw := signToken(t, fixture.privateKey, claims, test.includeKID, jwt.SigningMethodRS256)
			principal, err := fixture.verifier.Verify(
				context.Background(),
				raw,
				PurposeAccess,
			)
			if err == nil || principal.Authenticated {
				t.Fatalf("Verify() = %#v, %v", principal, err)
			}
		})
	}
}

func TestVerifierRejectsWrongAlgorithmAndUnknownKeys(t *testing.T) {
	fixture := newFixture(t)
	claims := fixture.validClaims()

	hsToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hsToken.Header["kid"] = "active-1"
	hsRaw, err := hsToken.SignedString([]byte("not-rsa"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.verifier.Verify(context.Background(), hsRaw, PurposeAccess); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify(HS256) error = %v", err)
	}

	unknown := signTokenWithKID(t, fixture.privateKey, claims, "unknown")
	if _, err := fixture.verifier.Verify(context.Background(), unknown, PurposeAccess); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Verify(unknown kid) error = %v", err)
	}
}

func TestVerifierRejectsTamperedTokens(t *testing.T) {
	fixture := newFixture(t)
	claims := fixture.validClaims()
	raw := signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	parts := strings.Split(raw, ".")
	claims.UserID = "another-user"
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "active-1"
	modifiedPayload, err := token.SigningString()
	if err != nil {
		t.Fatal(err)
	}
	for name, tampered := range map[string]string{
		"changed identity":  modifiedPayload + "." + parts[2],
		"removed signature": strings.Join(parts[:2], ".") + ".",
	} {
		t.Run(name, func(t *testing.T) {
			principal, err := fixture.verifier.Verify(context.Background(), tampered, PurposeAccess)
			if !errors.Is(err, ErrInvalidToken) || principal != Anonymous() {
				t.Fatalf("Verify(tampered token) = %#v, %v", principal, err)
			}
		})
	}
}

func TestVerifierCopiesVerificationKeys(t *testing.T) {
	fixture := newFixture(t)
	publicKey := &rsa.PublicKey{
		N: new(big.Int).Set(fixture.privateKey.N),
		E: fixture.privateKey.E,
	}
	verificationKeys := map[string]*rsa.PublicKey{"active-1": publicKey}
	verifier, err := NewVerifier(
		KeySet{VerificationKeys: verificationKeys},
		fixture.verifierOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	delete(verificationKeys, "active-1")
	publicKey.N.SetInt64(1)

	raw := signTokenWithKID(t, fixture.privateKey, fixture.validClaims(), "active-1")
	if _, err := verifier.Verify(context.Background(), raw, PurposeAccess); err != nil {
		t.Fatalf("Verify() after caller key set mutation error = %v", err)
	}
}

func TestVerifierUsesBoundedClockSkewAndRedactsRawToken(t *testing.T) {
	fixture := newFixture(t)
	claims := fixture.validClaims()
	claims.IssuedAt = jwt.NewNumericDate(fixture.now.Add(30 * time.Second))
	raw := signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	if _, err := fixture.verifier.Verify(context.Background(), raw, PurposeAccess); err != nil {
		t.Fatalf("Verify(within leeway) error = %v", err)
	}

	claims.IssuedAt = jwt.NewNumericDate(fixture.now.Add(2 * time.Minute))
	raw = signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	principal, err := fixture.verifier.Verify(context.Background(), raw, PurposeAccess)
	if !errors.Is(err, ErrInvalidToken) || principal.Authenticated {
		t.Fatalf("Verify(future issued-at) = %#v, %v", principal, err)
	}

	claims.IssuedAt = jwt.NewNumericDate(fixture.now.Add(-3 * time.Minute))
	claims.ExpiresAt = jwt.NewNumericDate(fixture.now.Add(-2 * time.Minute))
	raw = signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	principal, err = fixture.verifier.Verify(context.Background(), raw, PurposeAccess)
	if !errors.Is(err, ErrExpiredToken) || principal.Authenticated {
		t.Fatalf("Verify(expired) = %#v, %v", principal, err)
	}
	if strings.Contains(err.Error(), raw) {
		t.Fatal("verification error contains raw token")
	}
}

func TestVerifyRejectsInvalidInputs(t *testing.T) {
	fixture := newFixture(t)
	for name, verifier := range map[string]*JWTVerifier{
		"nil verifier":           nil,
		"uninitialized verifier": {},
	} {
		t.Run(name, func(t *testing.T) {
			principal, err := verifier.Verify(context.Background(), "token", PurposeAccess)
			if !errors.Is(err, ErrInvalidToken) || principal != Anonymous() {
				t.Fatalf("Verify() = %#v, %v", principal, err)
			}
		})
	}
	for _, test := range []struct {
		name    string
		ctx     context.Context
		raw     string
		purpose TokenPurpose
		want    error
	}{
		{name: "nil context", purpose: PurposeAccess, want: ErrInvalidToken},
		{name: "invalid expected purpose", ctx: context.Background(), purpose: "unknown", want: ErrInvalidPurpose},
		{name: "empty token", ctx: context.Background(), purpose: PurposeAccess, want: ErrInvalidToken},
		{name: "oversize token", ctx: context.Background(), raw: strings.Repeat("x", defaultMaxTokenBytes+1), purpose: PurposeAccess, want: ErrInvalidToken},
	} {
		t.Run(test.name, func(t *testing.T) {
			principal, err := fixture.verifier.Verify(test.ctx, test.raw, test.purpose)
			if !errors.Is(err, test.want) || principal != Anonymous() {
				t.Fatalf("Verify() = %#v, %v", principal, err)
			}
		})
	}
}

func TestVerifyHonorsCancellation(t *testing.T) {
	fixture := newFixture(t)
	raw := signTokenWithKID(t, fixture.privateKey, fixture.validClaims(), "active-1")
	for _, duringOperation := range []bool{false, true} {
		name := "before verification"
		if duringOperation {
			name = "during verification"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			options := fixture.verifierOptions()
			if duringOperation {
				options.Clock = func() time.Time { cancel(); return fixture.now }
			} else {
				cancel()
			}
			verifier, err := NewVerifier(KeySet{VerificationKeys: map[string]*rsa.PublicKey{
				"active-1": &fixture.privateKey.PublicKey,
			}}, options)
			if err != nil {
				t.Fatal(err)
			}
			principal, err := verifier.Verify(ctx, raw, PurposeAccess)
			if !errors.Is(err, context.Canceled) || principal != Anonymous() {
				t.Fatalf("Verify() = %#v, %v", principal, err)
			}
		})
	}
}

func TestVerifierRejectsExpiredTokenWithinLeeway(t *testing.T) {
	fixture := newFixture(t)
	claims := fixture.validClaims()
	claims.IssuedAt = jwt.NewNumericDate(fixture.now.Add(-2 * time.Minute))
	claims.ExpiresAt = jwt.NewNumericDate(fixture.now.Add(-30 * time.Second))
	raw := signTokenWithKID(t, fixture.privateKey, claims, "active-1")
	principal, err := fixture.verifier.Verify(context.Background(), raw, PurposeAccess)
	if !errors.Is(err, ErrExpiredToken) || principal != Anonymous() {
		t.Fatalf("Verify(expired within leeway) = %#v, %v", principal, err)
	}
}

func TestVerifierAcceptsRotationKeysAndRejectsRemovedKey(t *testing.T) {
	fixture := newFixture(t)
	newKey, err := rsa.GenerateKey(rand.Reader, minimumRSAKeyBits)
	if err != nil {
		t.Fatal(err)
	}
	keys := KeySet{VerificationKeys: map[string]*rsa.PublicKey{
		"old": &fixture.privateKey.PublicKey,
		"new": &newKey.PublicKey,
	}}
	oldToken := signTokenWithKID(t, fixture.privateKey, fixture.validClaims(), "old")
	newToken := signTokenWithKID(t, newKey, fixture.validClaims(), "new")
	verifier, err := NewVerifier(keys, fixture.verifierOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{oldToken, newToken} {
		if _, err := verifier.Verify(context.Background(), raw, PurposeAccess); err != nil {
			t.Fatalf("Verify(during rotation) error = %v", err)
		}
	}
	delete(keys.VerificationKeys, "old")
	verifier, err = NewVerifier(keys, fixture.verifierOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), oldToken, PurposeAccess); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Verify(removed key) error = %v", err)
	}
	if _, err := verifier.Verify(context.Background(), newToken, PurposeAccess); err != nil {
		t.Fatalf("Verify(new key) error = %v", err)
	}
}

func TestKeyForTokenRejectsUnsupportedMethod(t *testing.T) {
	verifier := &JWTVerifier{}
	for _, token := range []*jwt.Token{nil, jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{})} {
		if _, err := verifier.keyForToken(token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("keyForToken() error = %v", err)
		}
	}
}

type fixture struct {
	now        time.Time
	privateKey *rsa.PrivateKey
	issuer     *Issuer
	verifier   *JWTVerifier
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
	result.issuer, err = NewIssuer(result.issuerOptions())
	if err != nil {
		t.Fatal(err)
	}
	result.verifier, err = NewVerifier(
		KeySet{VerificationKeys: map[string]*rsa.PublicKey{"active-1": &testKey.PublicKey}},
		result.verifierOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f fixture) issuerOptions() IssuerOptions {
	return IssuerOptions{
		Issuer:     "example-service",
		Audience:   "example-client",
		KeyID:      "active-1",
		PrivateKey: f.privateKey,
		MaxTTL:     time.Hour,
		Clock:      func() time.Time { return f.now },
	}
}

func (f fixture) verifierOptions() VerifierOptions {
	return VerifierOptions{
		Issuer:        "example-service",
		Audience:      "example-client",
		Leeway:        time.Minute,
		MaxTokenBytes: 16 << 10,
		Clock:         func() time.Time { return f.now },
	}
}

func (f fixture) validClaims() Claims {
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "example-service",
			Subject:   "user:42",
			Audience:  jwt.ClaimStrings{"example-client"},
			ExpiresAt: jwt.NewNumericDate(f.now.Add(time.Minute)),
			IssuedAt:  jwt.NewNumericDate(f.now),
			ID:        "token-1",
		},
		Purpose:   PurposeAccess,
		UserID:    "42",
		SessionID: "session-1",
	}
}

func signToken(
	t testing.TB,
	privateKey *rsa.PrivateKey,
	claims Claims,
	includeKID bool,
	method jwt.SigningMethod,
) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
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
	t testing.TB,
	privateKey *rsa.PrivateKey,
	claims Claims,
	keyID string,
) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = keyID
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
