package password_test

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/security/password"
)

func BenchmarkArgon2id(b *testing.B) {
	hasher, err := password.New(password.DefaultPolicy())
	if err != nil {
		b.Fatal(err)
	}
	secret := []byte("benchmark-only-password")
	b.ReportAllocs()
	for range b.N {
		if _, err := hasher.Hash(context.Background(), secret); err != nil {
			b.Fatal(err)
		}
	}
}
