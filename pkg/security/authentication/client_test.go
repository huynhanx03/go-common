package authentication

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestDefaultJWTPolicy(t *testing.T) {
	config := DefaultJWT()
	if config.AccessTTLSeconds != 15*60 || config.RefreshTTLSeconds != 7*24*60*60 ||
		config.MaxTTLSeconds != 30*24*60*60 || config.MaxTokenBytes != 16<<10 {
		t.Fatalf("DefaultJWT() = %#v", config)
	}
}

func TestDefaultOptionsIssueAndVerify(t *testing.T) {
	fixture := newFixture(t)
	issuerOptions := DefaultIssuerOptions()
	issuerOptions.Issuer = "example-service"
	issuerOptions.Audience = "example-client"
	issuerOptions.KeyID = "active-1"
	issuerOptions.PrivateKey = fixture.privateKey
	issuerOptions.Clock = func() time.Time { return fixture.now }
	issuer, err := NewIssuer(issuerOptions)
	if err != nil {
		t.Fatalf("NewIssuer(default options) error = %v", err)
	}
	issued, err := issuer.Issue(context.Background(), IssueRequest{
		Purpose:   PurposeAccess,
		Subject:   "user:42",
		UserID:    "42",
		SessionID: "session-1",
		IssuedAt:  fixture.now,
		ExpiresAt: fixture.now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.TokenID == "" {
		t.Fatal("Issue() did not generate a token ID")
	}

	verifierOptions := DefaultVerifierOptions()
	verifierOptions.Issuer = issuerOptions.Issuer
	verifierOptions.Audience = issuerOptions.Audience
	verifierOptions.Clock = issuerOptions.Clock
	verifier, err := NewVerifier(
		KeySet{VerificationKeys: map[string]*rsa.PublicKey{
			issuerOptions.KeyID: &fixture.privateKey.PublicKey,
		}},
		verifierOptions,
	)
	if err != nil {
		t.Fatalf("NewVerifier(default options) error = %v", err)
	}
	principal, err := verifier.Verify(context.Background(), issued.Raw, PurposeAccess)
	if err != nil || !principal.Authenticated || principal.TokenID != issued.TokenID {
		t.Fatalf("Verify() = %#v, %v", principal, err)
	}
}

func TestNewIssuerRejectsInvalidConfiguration(t *testing.T) {
	fixture := newFixture(t)
	for name, mutate := range map[string]func(*IssuerOptions){
		"missing issuer":    func(o *IssuerOptions) { o.Issuer = "" },
		"invalid audience":  func(o *IssuerOptions) { o.Audience = " api" },
		"overlong issuer":   func(o *IssuerOptions) { o.Issuer = strings.Repeat("a", 257) },
		"control character": func(o *IssuerOptions) { o.Issuer = "api\x00issuer" },
		"missing key ID":    func(o *IssuerOptions) { o.KeyID = "" },
		"overlong key ID":   func(o *IssuerOptions) { o.KeyID = strings.Repeat("a", 129) },
		"invalid key ID":    func(o *IssuerOptions) { o.KeyID = "key/1" },
		"missing key":       func(o *IssuerOptions) { o.PrivateKey = nil },
		"missing modulus":   func(o *IssuerOptions) { o.PrivateKey = &rsa.PrivateKey{} },
		"weak key": func(o *IssuerOptions) {
			o.PrivateKey = &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: big.NewInt(17), E: 3}}
		},
		"invalid key": func(o *IssuerOptions) {
			o.PrivateKey = &rsa.PrivateKey{PublicKey: fixture.privateKey.PublicKey}
		},
		"missing maximum TTL":   func(o *IssuerOptions) { o.MaxTTL = 0 },
		"negative maximum TTL":  func(o *IssuerOptions) { o.MaxTTL = -time.Second },
		"excessive maximum TTL": func(o *IssuerOptions) { o.MaxTTL = 31 * 24 * time.Hour },
	} {
		t.Run(name, func(t *testing.T) {
			options := fixture.issuerOptions()
			mutate(&options)
			issuer, err := NewIssuer(options)
			if !errors.Is(err, ErrInvalidConfiguration) || issuer != nil {
				t.Fatalf("NewIssuer() = %v, %v", issuer, err)
			}
		})
	}
}

func TestNewVerifierRejectsInvalidConfigurationAndKeySets(t *testing.T) {
	fixture := newFixture(t)
	tests := []struct {
		name        string
		options     func(*VerifierOptions)
		keyID       string
		key         *rsa.PublicKey
		emptyKeySet bool
		want        error
	}{
		{name: "missing issuer", options: func(o *VerifierOptions) { o.Issuer = "" }, want: ErrInvalidConfiguration},
		{name: "invalid audience", options: func(o *VerifierOptions) { o.Audience = "api\x00audience" }, want: ErrInvalidConfiguration},
		{name: "negative leeway", options: func(o *VerifierOptions) { o.Leeway = -time.Second }, want: ErrInvalidConfiguration},
		{name: "excessive leeway", options: func(o *VerifierOptions) { o.Leeway = 6 * time.Minute }, want: ErrInvalidConfiguration},
		{name: "small byte limit", options: func(o *VerifierOptions) { o.MaxTokenBytes = 255 }, want: ErrInvalidConfiguration},
		{name: "excessive byte limit", options: func(o *VerifierOptions) { o.MaxTokenBytes = 64<<10 + 1 }, want: ErrInvalidConfiguration},
		{name: "empty key set", emptyKeySet: true, want: ErrInvalidKeySet},
		{name: "missing key ID", key: &fixture.privateKey.PublicKey, want: ErrInvalidKeySet},
		{name: "invalid key ID", keyID: "bad/key", key: &fixture.privateKey.PublicKey, want: ErrInvalidKeySet},
		{name: "nil key", keyID: "key-1", want: ErrInvalidKeySet},
		{name: "missing modulus", keyID: "key-1", key: &rsa.PublicKey{E: 65537}, want: ErrInvalidKeySet},
		{name: "weak modulus", keyID: "key-1", key: &rsa.PublicKey{N: big.NewInt(17), E: 3}, want: ErrInvalidKeySet},
		{name: "invalid exponent", keyID: "key-1", key: &rsa.PublicKey{N: fixture.privateKey.N, E: 1}, want: ErrInvalidKeySet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := fixture.verifierOptions()
			if test.options != nil {
				test.options(&options)
			}
			keys := KeySet{VerificationKeys: map[string]*rsa.PublicKey{test.keyID: test.key}}
			if test.emptyKeySet {
				keys.VerificationKeys = nil
			}
			verifier, err := NewVerifier(keys, options)
			if !errors.Is(err, test.want) || verifier != nil {
				t.Fatalf("NewVerifier() = %v, %v; want %v", verifier, err, test.want)
			}
		})
	}
}

func TestIssuerOwnsSigningKey(t *testing.T) {
	fixture := newFixture(t)
	key, err := x509.ParsePKCS1PrivateKey(x509.MarshalPKCS1PrivateKey(fixture.privateKey))
	if err != nil {
		t.Fatal(err)
	}
	options := fixture.issuerOptions()
	options.PrivateKey = key
	issuer, err := NewIssuer(options)
	if err != nil {
		t.Fatal(err)
	}
	key.N.SetInt64(1)
	key.D.SetInt64(1)
	for _, prime := range key.Primes {
		prime.SetInt64(1)
	}
	key.Precomputed.Dp.SetInt64(1)
	key.Precomputed.Dq.SetInt64(1)
	key.Precomputed.Qinv.SetInt64(1)
	issued, err := issuer.Issue(context.Background(), validIssueRequest(fixture.now))
	if err != nil {
		t.Fatalf("Issue() after caller key mutation: %v", err)
	}
	if _, err := fixture.verifier.Verify(context.Background(), issued.Raw, PurposeAccess); err != nil {
		t.Fatalf("Verify() after caller key mutation: %v", err)
	}
}
