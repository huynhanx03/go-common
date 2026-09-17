package jwt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func TestNewIssuerRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	privateKey := configurationTestKey(t)
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	valid := IssuerOptions{
		Issuer:     "example-service",
		Audience:   "example-client",
		KeyID:      "active-1",
		PrivateKey: privateKey,
		MaxTTL:     time.Hour,
	}
	tests := []struct {
		name   string
		mutate func(*IssuerOptions)
	}{
		{name: "empty issuer", mutate: func(options *IssuerOptions) { options.Issuer = "" }},
		{name: "invalid audience", mutate: func(options *IssuerOptions) { options.Audience = " web " }},
		{name: "invalid key ID", mutate: func(options *IssuerOptions) { options.KeyID = "bad/key" }},
		{name: "nil private key", mutate: func(options *IssuerOptions) { options.PrivateKey = nil }},
		{name: "weak private key", mutate: func(options *IssuerOptions) { options.PrivateKey = weakKey }},
		{name: "zero max TTL", mutate: func(options *IssuerOptions) { options.MaxTTL = 0 }},
		{name: "excessive max TTL", mutate: func(options *IssuerOptions) { options.MaxTTL = hardMaxTTL + time.Second }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := valid
			test.mutate(&options)
			if _, issueErr := NewIssuer(options); issueErr == nil ||
				(!errors.Is(issueErr, authentication.ErrInvalidConfiguration) &&
					!errors.Is(issueErr, authentication.ErrInvalidKeySet)) {
				t.Fatalf("NewIssuer() error = %v", issueErr)
			}
		})
	}
}

func TestIssuerRejectsInvalidRequestsAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	issuer, err := NewIssuer(IssuerOptions{
		Issuer:     "example-service",
		Audience:   "example-client",
		KeyID:      "active-1",
		PrivateKey: configurationTestKey(t),
		MaxTTL:     time.Hour,
		Clock:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := authentication.IssueRequest{
		Purpose:   authentication.PurposeAccess,
		Subject:   "user:1",
		UserID:    "1",
		SessionID: "session-1",
		Username:  "alice",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Minute),
	}

	tests := []struct {
		name   string
		mutate func(*authentication.IssueRequest)
	}{
		{name: "invalid purpose", mutate: func(request *authentication.IssueRequest) { request.Purpose = "other" }},
		{name: "missing subject", mutate: func(request *authentication.IssueRequest) { request.Subject = "" }},
		{name: "missing user ID", mutate: func(request *authentication.IssueRequest) { request.UserID = "" }},
		{name: "missing session ID", mutate: func(request *authentication.IssueRequest) { request.SessionID = "" }},
		{name: "padded username", mutate: func(request *authentication.IssueRequest) { request.Username = " alice" }},
		{name: "control character", mutate: func(request *authentication.IssueRequest) { request.TokenID = "bad\nid" }},
		{name: "oversized claim", mutate: func(request *authentication.IssueRequest) { request.Subject = strings.Repeat("x", maxClaimBytes+1) }},
		{name: "zero issued at", mutate: func(request *authentication.IssueRequest) { request.IssuedAt = time.Time{} }},
		{name: "zero expiry", mutate: func(request *authentication.IssueRequest) { request.ExpiresAt = time.Time{} }},
		{name: "security version without auth time", mutate: func(request *authentication.IssueRequest) { request.SecurityVersion = 1 }},
		{name: "auth time without security version", mutate: func(request *authentication.IssueRequest) { request.AuthenticatedAt = now }},
		{name: "auth time after issue", mutate: func(request *authentication.IssueRequest) {
			request.AuthenticatedAt = now.Add(maxIssueClockSkew + time.Second)
			request.SecurityVersion = 1
		}},
		{name: "future issued at", mutate: func(request *authentication.IssueRequest) {
			request.IssuedAt = now.Add(maxIssueClockSkew + time.Second)
			request.ExpiresAt = request.IssuedAt.Add(time.Minute)
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := valid
			test.mutate(&request)
			if _, issueErr := issuer.Issue(context.Background(), request); !errors.Is(
				issueErr,
				authentication.ErrIssueRejected,
			) {
				t.Fatalf("Issue() error = %v", issueErr)
			}
		})
	}

	if _, issueErr := issuer.Issue(nil, valid); !errors.Is(issueErr, authentication.ErrIssueRejected) {
		t.Fatalf("Issue(nil context) error = %v", issueErr)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, issueErr := issuer.Issue(cancelled, valid); !errors.Is(issueErr, context.Canceled) {
		t.Fatalf("Issue(cancelled) error = %v", issueErr)
	}

	lateCancelled, lateCancel := context.WithCancel(context.Background())
	lateIssuer, err := NewIssuer(IssuerOptions{
		Issuer:     "example-service",
		Audience:   "example-client",
		KeyID:      "active-1",
		PrivateKey: configurationTestKey(t),
		MaxTTL:     time.Hour,
		Clock: func() time.Time {
			lateCancel()
			return now
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, issueErr := lateIssuer.Issue(lateCancelled, valid); !errors.Is(issueErr, context.Canceled) {
		t.Fatalf("Issue(cancelled during issue) error = %v", issueErr)
	}
}

func TestNewVerifierRejectsUnsafeOptionsAndKeySets(t *testing.T) {
	t.Parallel()

	privateKey := configurationTestKey(t)
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	validKeys := KeySet{Active: map[string]*rsa.PublicKey{"active-1": &privateKey.PublicKey}}
	validOptions := VerifierOptions{Issuer: "example-service", Audience: "example-client"}

	optionTests := []struct {
		name   string
		mutate func(*VerifierOptions)
	}{
		{name: "empty issuer", mutate: func(options *VerifierOptions) { options.Issuer = "" }},
		{name: "control audience", mutate: func(options *VerifierOptions) { options.Audience = "web\n" }},
		{name: "negative leeway", mutate: func(options *VerifierOptions) { options.Leeway = -time.Second }},
		{name: "excessive leeway", mutate: func(options *VerifierOptions) { options.Leeway = hardMaxLeeway + time.Second }},
		{name: "tiny token limit", mutate: func(options *VerifierOptions) { options.MaxTokenBytes = 255 }},
		{name: "excessive token limit", mutate: func(options *VerifierOptions) { options.MaxTokenBytes = hardMaxTokenBytes + 1 }},
	}
	for _, test := range optionTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := validOptions
			test.mutate(&options)
			if _, verifyErr := NewVerifier(validKeys, options); !errors.Is(
				verifyErr,
				authentication.ErrInvalidConfiguration,
			) {
				t.Fatalf("NewVerifier() error = %v", verifyErr)
			}
		})
	}

	keyTests := []struct {
		name string
		keys KeySet
	}{
		{name: "no active keys", keys: KeySet{}},
		{name: "invalid active ID", keys: KeySet{Active: map[string]*rsa.PublicKey{"bad/id": &privateKey.PublicKey}}},
		{name: "nil active key", keys: KeySet{Active: map[string]*rsa.PublicKey{"active-1": nil}}},
		{name: "weak active key", keys: KeySet{Active: map[string]*rsa.PublicKey{"active-1": &weakKey.PublicKey}}},
		{name: "duplicate retired ID", keys: KeySet{Active: map[string]*rsa.PublicKey{"same": &privateKey.PublicKey}, Retired: map[string]*rsa.PublicKey{"same": &privateKey.PublicKey}}},
		{name: "invalid retired ID", keys: KeySet{Active: map[string]*rsa.PublicKey{"active-1": &privateKey.PublicKey}, Retired: map[string]*rsa.PublicKey{"bad/id": &privateKey.PublicKey}}},
		{name: "nil retired key", keys: KeySet{Active: map[string]*rsa.PublicKey{"active-1": &privateKey.PublicKey}, Retired: map[string]*rsa.PublicKey{"retired-1": nil}}},
	}
	for _, test := range keyTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, verifyErr := NewVerifier(test.keys, validOptions); !errors.Is(
				verifyErr,
				authentication.ErrInvalidKeySet,
			) {
				t.Fatalf("NewVerifier() error = %v", verifyErr)
			}
		})
	}

	invalidExponent := &rsa.PublicKey{N: privateKey.N, E: 1}
	if validateErr := validatePublicKey(invalidExponent); !errors.Is(
		validateErr,
		authentication.ErrInvalidKeySet,
	) {
		t.Fatalf("validatePublicKey(invalid exponent) error = %v", validateErr)
	}
}

func TestVerifierRejectsInvalidBoundariesAndClaims(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	privateKey := configurationTestKey(t)
	verifier, err := NewVerifier(
		KeySet{Active: map[string]*rsa.PublicKey{"active-1": &privateKey.PublicKey}},
		VerifierOptions{
			Issuer:   "example-service",
			Audience: "example-client",
			Clock:    func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, verifyErr := verifier.Verify(nil, "token", authentication.PurposeAccess); !errors.Is(
		verifyErr,
		authentication.ErrInvalidToken,
	) {
		t.Fatalf("Verify(nil context) error = %v", verifyErr)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, verifyErr := verifier.Verify(cancelled, "token", authentication.PurposeAccess); !errors.Is(
		verifyErr,
		context.Canceled,
	) {
		t.Fatalf("Verify(cancelled) error = %v", verifyErr)
	}
	if _, verifyErr := verifier.Verify(context.Background(), "token", "other"); !errors.Is(
		verifyErr,
		authentication.ErrInvalidPurpose,
	) {
		t.Fatalf("Verify(invalid purpose) error = %v", verifyErr)
	}
	for _, raw := range []string{"", strings.Repeat("x", defaultMaxTokenBytes+1)} {
		if _, verifyErr := verifier.Verify(context.Background(), raw, authentication.PurposeAccess); !errors.Is(
			verifyErr,
			authentication.ErrInvalidToken,
		) {
			t.Fatalf("Verify(boundary) error = %v", verifyErr)
		}
	}

	claims := validConfigurationClaims(now)
	claims.Type = authentication.PurposeRefresh
	raw := signConfigurationToken(t, privateKey, claims, "active-1")
	if _, verifyErr := verifier.Verify(context.Background(), raw, authentication.PurposeAccess); !errors.Is(
		verifyErr,
		authentication.ErrInvalidToken,
	) {
		t.Fatalf("Verify(type mismatch) error = %v", verifyErr)
	}

	if _, keyErr := verifier.keyForToken(nil); !errors.Is(keyErr, authentication.ErrInvalidToken) {
		t.Fatalf("keyForToken(nil) error = %v", keyErr)
	}
	wrongMethod := jwtlib.New(jwtlib.SigningMethodHS256)
	if _, keyErr := verifier.keyForToken(wrongMethod); !errors.Is(keyErr, authentication.ErrInvalidToken) {
		t.Fatalf("keyForToken(wrong method) error = %v", keyErr)
	}
}

func TestClaimAndIdentifierValidation(t *testing.T) {
	t.Parallel()

	if err := validateVerifiedClaims(nil); !errors.Is(err, authentication.ErrInvalidToken) {
		t.Fatalf("validateVerifiedClaims(nil) error = %v", err)
	}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	base := validConfigurationClaims(now)
	tests := []struct {
		name   string
		mutate func(*Claims)
	}{
		{name: "empty subject", mutate: func(claims *Claims) { claims.Subject = "" }},
		{name: "empty user ID", mutate: func(claims *Claims) { claims.UserID = "" }},
		{name: "empty session", mutate: func(claims *Claims) { claims.SessionID = "" }},
		{name: "invalid username", mutate: func(claims *Claims) { claims.Username = " alice" }},
		{name: "invalid token ID", mutate: func(claims *Claims) { claims.ID = "bad\nid" }},
		{name: "invalid interval", mutate: func(claims *Claims) { claims.ExpiresAt = jwtlib.NewNumericDate(now) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			claims := base
			test.mutate(&claims)
			if err := validateVerifiedClaims(&claims); !errors.Is(err, authentication.ErrInvalidToken) {
				t.Fatalf("validateVerifiedClaims() error = %v", err)
			}
		})
	}

	if err := validateKeyID(strings.Repeat("x", maxKeyIDBytes+1)); !errors.Is(
		err,
		authentication.ErrInvalidKeySet,
	) {
		t.Fatalf("validateKeyID(too long) error = %v", err)
	}
	if err := validateIdentifier("issuer", strings.Repeat("x", maxClaimBytes+1)); !errors.Is(
		err,
		authentication.ErrInvalidConfiguration,
	) {
		t.Fatalf("validateIdentifier(too long) error = %v", err)
	}
}

func validConfigurationClaims(now time.Time) Claims {
	return Claims{
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    "example-service",
			Subject:   "user:1",
			Audience:  jwtlib.ClaimStrings{"example-client"},
			ExpiresAt: jwtlib.NewNumericDate(now.Add(time.Minute)),
			IssuedAt:  jwtlib.NewNumericDate(now),
			ID:        "token-1",
		},
		Purpose:   authentication.PurposeAccess,
		UserID:    "1",
		SessionID: "session-1",
		Username:  "alice",
	}
}

func signConfigurationToken(
	t *testing.T,
	privateKey *rsa.PrivateKey,
	claims Claims,
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

func configurationTestKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}
