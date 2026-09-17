package jwt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func FuzzVerify(f *testing.F) {
	fixture := newFixture(f)
	issued, err := fixture.issuer.Issue(context.Background(), authentication.IssueRequest{
		Purpose:   authentication.PurposeAccess,
		Subject:   "user:1",
		UserID:    "1",
		SessionID: "session-1",
		IssuedAt:  fixture.now,
		ExpiresAt: fixture.now.Add(60_000_000_000),
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add("")
	f.Add("not-a-jwt")
	f.Add("super-secret-token-material")
	f.Add(issued.Raw)

	f.Fuzz(func(t *testing.T, raw string) {
		principal, err := fixture.verifier.Verify(
			context.Background(),
			raw,
			authentication.PurposeAccess,
		)
		if err != nil {
			if principal.Authenticated {
				t.Fatalf("authenticated principal on error: %#v", principal)
			}
			// Very short inputs can occur naturally in a static error such as
			// "invalid token"; only a meaningful raw fragment indicates a leak.
			if len(raw) >= 16 && strings.Contains(err.Error(), raw) {
				t.Fatal("error contains raw token")
			}
		}
	})
}
