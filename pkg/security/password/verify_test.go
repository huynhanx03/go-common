package password

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestVerifyHandlesArgon2idMatchMismatchAndRehash(t *testing.T) {
	t.Parallel()

	oldHasher := newTestHasher(t, testPasswordSettings(32, 1, 1, 1))
	encoded, err := oldHasher.Hash(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	currentHasher := newTestHasher(t, testPasswordSettings(64, 2, 1, 1))
	result, err := currentHasher.Verify(context.Background(), encoded, []byte("secret"))
	if err != nil || !result.Valid || !result.NeedsRehash || result.Algorithm != argon2idAlgorithm {
		t.Fatalf("Verify() = %+v, %v; want valid Argon2id hash requiring rehash", result, err)
	}
	result, err = currentHasher.Verify(context.Background(), encoded, []byte("incorrect"))
	if err != nil || result.Valid || result.NeedsRehash || result.Algorithm != argon2idAlgorithm {
		t.Fatalf("Verify() mismatch = %+v, %v", result, err)
	}
}

func TestVerifyRejectsInvalidInputAndUnsupportedHash(t *testing.T) {
	t.Parallel()

	hasher := newTestHasher(t, testPasswordSettings(32, 1, 1, 1))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name     string
		ctx      context.Context
		encoded  string
		password []byte
		want     error
	}{
		{name: "nil context", encoded: "unknown", password: []byte("secret"), want: ErrInvalidContext},
		{name: "canceled context", ctx: canceled, encoded: "unknown", password: []byte("secret"), want: context.Canceled},
		{name: "empty password", ctx: context.Background(), encoded: "unknown", want: ErrEmptyPassword},
		{name: "oversized password", ctx: context.Background(), encoded: "unknown", password: make([]byte, defaultMaxPasswordBytes+1), want: ErrPasswordTooLong},
		{name: "encoded hash too long", ctx: context.Background(), encoded: strings.Repeat("x", defaultMaxPHCBytes+1), password: []byte("secret"), want: ErrHashPolicy},
		{name: "unsupported algorithm", ctx: context.Background(), encoded: "unknown", password: []byte("secret"), want: ErrUnsupportedHash},
		{name: "malformed Argon2id", ctx: context.Background(), encoded: "$argon2id$broken", password: []byte("secret"), want: ErrMalformedHash},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := hasher.Verify(test.ctx, test.encoded, test.password)
			if !errors.Is(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestVerifyArgon2idHonorsConcurrencyCancellation(t *testing.T) {
	t.Parallel()

	hasher := newTestHasher(t, testPasswordSettings(32, 1, 1, 1))
	encoded, err := hasher.Hash(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	hasher.slots <- struct{}{}
	if _, err := hasher.Verify(&doneContext{Context: context.Background()}, encoded, []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify() while full = %v, want context.Canceled", err)
	}
}

func TestVerifyBcryptCompatibilityAndPolicy(t *testing.T) {
	t.Parallel()

	hasher := newTestHasher(t, DefaultPassword())
	encoded, err := bcrypt.GenerateFromPassword([]byte("secret"), minBcryptCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}

	result, err := hasher.Verify(context.Background(), string(encoded), []byte("secret"))
	if err != nil || !result.Valid || !result.NeedsRehash || result.Algorithm != bcryptAlgorithm {
		t.Fatalf("Verify() bcrypt = %+v, %v; want valid legacy hash requiring rehash", result, err)
	}
	result, err = hasher.Verify(context.Background(), string(encoded), []byte("incorrect"))
	if err != nil || result.Valid || result.NeedsRehash || result.Algorithm != bcryptAlgorithm {
		t.Fatalf("Verify() bcrypt mismatch = %+v, %v", result, err)
	}
	for _, prefix := range []string{"$2a$", "$2b$", "$2y$"} {
		variant := append([]byte(nil), encoded...)
		copy(variant[:4], prefix)
		result, err := hasher.Verify(context.Background(), string(variant), []byte("secret"))
		if err != nil || !result.Valid {
			t.Errorf("Verify() rejected bcrypt prefix %q: result=%+v err=%v", prefix, result, err)
		}
	}

	tooCheap := append([]byte(nil), encoded...)
	copy(tooCheap[4:6], "04")
	if _, err := hasher.Verify(context.Background(), string(tooCheap), []byte("secret")); !errors.Is(err, ErrHashPolicy) {
		t.Fatalf("Verify() accepted bcrypt below minimum cost: %v", err)
	}
	tooExpensive := append([]byte(nil), encoded...)
	copy(tooExpensive[4:6], "15")
	if _, err := hasher.Verify(context.Background(), string(tooExpensive), []byte("secret")); !errors.Is(err, ErrHashPolicy) {
		t.Fatalf("Verify() accepted bcrypt above maximum cost: %v", err)
	}
	if _, err := hasher.Verify(context.Background(), string(encoded), make([]byte, maxBcryptPasswordBytes+1)); !errors.Is(err, ErrHashPolicy) {
		t.Fatalf("Verify() accepted password above bcrypt limit: %v", err)
	}
	if _, err := hasher.Verify(context.Background(), "$2a$bad", []byte("secret")); !errors.Is(err, ErrMalformedHash) {
		t.Fatalf("Verify() error for malformed bcrypt = %v", err)
	}
	corrupt := append([]byte(nil), encoded...)
	corrupt[7] = '!'
	if _, err := hasher.Verify(context.Background(), string(corrupt), []byte("secret")); !errors.Is(err, ErrMalformedHash) {
		t.Fatalf("Verify() error for corrupt bcrypt payload = %v", err)
	}
}

func TestVerifyBcryptHonorsConcurrencyCancellation(t *testing.T) {
	t.Parallel()

	hasher := newTestHasher(t, DefaultPassword())
	encoded, err := bcrypt.GenerateFromPassword([]byte("secret"), minBcryptCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	for range cap(hasher.slots) {
		hasher.slots <- struct{}{}
	}
	if _, err := hasher.Verify(&doneContext{Context: context.Background()}, string(encoded), []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify() while full = %v, want context.Canceled", err)
	}
}

func TestIsBcryptRecognizesSupportedPrefixes(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{"$2a$", "$2b$", "$2y$"} {
		if !isBcrypt(prefix + "anything") {
			t.Errorf("isBcrypt(%q) = false", prefix)
		}
	}
	for _, value := range []string{"", "$argon2id$", "$2x$", "2a$"} {
		if isBcrypt(value) {
			t.Errorf("isBcrypt(%q) = true", value)
		}
	}
}
