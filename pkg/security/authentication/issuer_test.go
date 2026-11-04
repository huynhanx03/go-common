package authentication

import (
	"context"
	"crypto/rsa"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIssuerRejectsInvalidRequests(t *testing.T) {
	fixture := newFixture(t)
	request := IssueRequest{
		Purpose:   PurposeAccess,
		Subject:   "user:42",
		UserID:    "42",
		SessionID: "session-1",
		IssuedAt:  fixture.now,
		ExpiresAt: fixture.now.Add(time.Minute),
	}
	for name, mutate := range map[string]func(*IssueRequest){
		"unknown purpose":   func(r *IssueRequest) { r.Purpose = "unknown" },
		"missing subject":   func(r *IssueRequest) { r.Subject = "" },
		"padded user":       func(r *IssueRequest) { r.UserID = " 42" },
		"control character": func(r *IssueRequest) { r.SessionID = "session\x00id" },
		"overlong username": func(r *IssueRequest) { r.Username = strings.Repeat("a", 257) },
		"invalid token ID":  func(r *IssueRequest) { r.TokenID = " " },
		"overlong lifetime": func(r *IssueRequest) { r.ExpiresAt = fixture.now.Add(2 * time.Hour) },
		"invalid interval":  func(r *IssueRequest) { r.ExpiresAt = fixture.now },
		"missing auth time": func(r *IssueRequest) { r.SecurityVersion = 1 },
		"future authentication": func(r *IssueRequest) {
			r.AuthenticatedAt = fixture.now.Add(2 * time.Minute)
			r.SecurityVersion = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			mutate(&candidate)
			if _, err := fixture.issuer.Issue(context.Background(), candidate); !errors.Is(err, ErrIssueRejected) {
				t.Fatalf("Issue() error = %v", err)
			}
		})
	}
}

func TestIssueRejectsInvalidDependencies(t *testing.T) {
	fixture := newFixture(t)
	for name, issuer := range map[string]*Issuer{
		"nil issuer":           nil,
		"uninitialized issuer": {},
	} {
		t.Run(name, func(t *testing.T) {
			issued, err := issuer.Issue(context.Background(), validIssueRequest(fixture.now))
			if !errors.Is(err, ErrIssueRejected) || issued != (IssuedToken{}) {
				t.Fatalf("Issue() = %#v, %v", issued, err)
			}
		})
	}
	if _, err := fixture.issuer.Issue(nil, validIssueRequest(fixture.now)); !errors.Is(err, ErrIssueRejected) {
		t.Fatalf("Issue(nil context) error = %v", err)
	}
}

func TestIssueHonorsCancellation(t *testing.T) {
	fixture := newFixture(t)
	for _, duringOperation := range []bool{false, true} {
		name := "before issuing"
		if duringOperation {
			name = "during issuing"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			options := fixture.issuerOptions()
			if duringOperation {
				options.Clock = func() time.Time { cancel(); return fixture.now }
			} else {
				cancel()
			}
			issuer, err := NewIssuer(options)
			if err != nil {
				t.Fatal(err)
			}
			issued, err := issuer.Issue(ctx, validIssueRequest(fixture.now))
			if !errors.Is(err, context.Canceled) || issued != (IssuedToken{}) {
				t.Fatalf("Issue() = %#v, %v", issued, err)
			}
		})
	}
}

func TestIssueReturnsNoTokenWhenGenerationOrSigningFails(t *testing.T) {
	fixture := newFixture(t)
	t.Run("random source fails", func(t *testing.T) {
		uuid.SetRand(failingReader{errors.New("random source unavailable")})
		t.Cleanup(func() { uuid.SetRand(nil) })
		request := validIssueRequest(fixture.now)
		request.TokenID = ""
		issued, err := fixture.issuer.Issue(context.Background(), request)
		if !errors.Is(err, ErrIssueRejected) || issued != (IssuedToken{}) {
			t.Fatalf("Issue() = %#v, %v", issued, err)
		}
	})
	t.Run("RSA signer fails", func(t *testing.T) {
		issuer := *fixture.issuer
		issuer.privateKey = &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: big.NewInt(17), E: 3}}
		issued, err := issuer.Issue(context.Background(), validIssueRequest(fixture.now))
		if !errors.Is(err, ErrIssueRejected) || issued != (IssuedToken{}) {
			t.Fatalf("Issue() = %#v, %v", issued, err)
		}
	})
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func validIssueRequest(now time.Time) IssueRequest {
	return IssueRequest{
		Purpose:   PurposeAccess,
		Subject:   "user:42",
		UserID:    "42",
		SessionID: "session-1",
		TokenID:   "token-1",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Minute),
	}
}
