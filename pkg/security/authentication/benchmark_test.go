//go:build benchmark

package authentication

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var benchmarkIssuedToken IssuedToken
var benchmarkPrincipal Principal

func BenchmarkIssue(b *testing.B) {
	fixture := newFixture(b)
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		tokenID string
	}{
		{name: "generated-ID"},
		{name: "provided-ID", tokenID: "token-1"},
	} {
		b.Run(test.name, func(b *testing.B) {
			request := validIssueRequest(fixture.now)
			request.TokenID = test.tokenID
			var result IssuedToken
			b.ReportAllocs()
			for b.Loop() {
				var err error
				result, err = fixture.issuer.Issue(ctx, request)
				if err != nil {
					b.Fatal(err)
				}
			}
			benchmarkIssuedToken = result
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "tokens/s")
		})
	}
}

func BenchmarkVerify(b *testing.B) {
	fixture := newFixture(b)
	ctx := context.Background()
	issued, err := fixture.issuer.Issue(ctx, validIssueRequest(fixture.now))
	if err != nil {
		b.Fatal(err)
	}
	parts := strings.Split(issued.Raw, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, fixture.privateKey.Size()))
	invalidSignature := strings.Join(parts, ".")
	expiredClaims := fixture.validClaims()
	expiredClaims.IssuedAt = jwt.NewNumericDate(fixture.now.Add(-3 * time.Minute))
	expiredClaims.ExpiresAt = jwt.NewNumericDate(fixture.now.Add(-2 * time.Minute))
	expired := signTokenWithKID(b, fixture.privateKey, expiredClaims, "active-1")
	for _, test := range []struct {
		name  string
		raw   string
		valid bool
	}{
		{name: "valid", raw: issued.Raw, valid: true},
		{name: "invalid-signature", raw: invalidSignature},
		{name: "expired", raw: expired},
		{name: "malformed", raw: "not-a-jwt"},
	} {
		b.Run(test.name, func(b *testing.B) {
			var result Principal
			b.ReportAllocs()
			for b.Loop() {
				var err error
				result, err = fixture.verifier.Verify(ctx, test.raw, PurposeAccess)
				if (err == nil) != test.valid || result.Authenticated != test.valid {
					b.Fatalf("Verify() = %#v, %v", result, err)
				}
			}
			benchmarkPrincipal = result
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "tokens/s")
		})
	}
}

func BenchmarkVerifyParallel(b *testing.B) {
	fixture := newFixture(b)
	ctx := context.Background()
	issued, err := fixture.issuer.Issue(ctx, validIssueRequest(fixture.now))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			principal, err := fixture.verifier.Verify(ctx, issued.Raw, PurposeAccess)
			if err != nil || !principal.Authenticated {
				b.Errorf("Verify() = %#v, %v", principal, err)
				return
			}
		}
	})
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "tokens/s")
}
