package security_test

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/huynhanx03/go-common/pkg/security"
)

func TestPasswordCompatibilityFacadeUsesArgon2id(t *testing.T) {
	encoded, err := security.HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$") {
		t.Fatalf("HashPassword() = %q, want Argon2id", encoded)
	}
	if err := security.ComparePassword(encoded, "secret"); err != nil {
		t.Fatalf("ComparePassword() error = %v", err)
	}
	if err := security.ComparePassword(encoded, "wrong"); !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Fatalf("ComparePassword(wrong) error = %v", err)
	}
}

func TestPasswordCompatibilityFacadeAcceptsCurrentBcrypt(t *testing.T) {
	encoded, err := bcrypt.GenerateFromPassword([]byte("secret"), security.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.ComparePassword(string(encoded), "secret"); err != nil {
		t.Fatalf("ComparePassword(bcrypt) error = %v", err)
	}
}
