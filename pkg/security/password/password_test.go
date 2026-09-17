package password_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/huynhanx03/go-common/pkg/security/password"
)

func TestHasherHashAndVerifyPreservesExactBytes(t *testing.T) {
	hasher, err := password.New(testPolicy())
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := hasher.Hash(context.Background(), []byte(" password "))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	verification, err := hasher.Verify(context.Background(), encoded, []byte(" password "))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !verification.Valid || verification.NeedsRehash || verification.Algorithm != "argon2id" {
		t.Fatalf("verification = %#v", verification)
	}

	trimmed, err := hasher.Verify(context.Background(), encoded, []byte("password"))
	if err != nil {
		t.Fatalf("Verify(trimmed) error = %v", err)
	}
	if trimmed.Valid {
		t.Fatal("Verify(trimmed).Valid = true, password bytes were normalized")
	}
}

func TestHasherRejectsEmptyAndOversizedPasswords(t *testing.T) {
	policy := testPolicy()
	policy.MaxPasswordBytes = 4
	hasher, err := password.New(policy)
	if err != nil {
		t.Fatal(err)
	}

	for _, candidate := range [][]byte{nil, {}, []byte("12345")} {
		if _, err := hasher.Hash(context.Background(), candidate); err == nil {
			t.Fatalf("Hash(%q) error = nil", candidate)
		}
	}
}

func TestHasherRejectsNilAndCancelledContexts(t *testing.T) {
	hasher, err := password.New(testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hasher.Hash(nil, []byte("secret")); !errors.Is(err, password.ErrInvalidContext) {
		t.Fatalf("Hash(nil) error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hasher.Hash(ctx, []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hash(cancelled) error = %v", err)
	}
	if _, err := hasher.Verify(ctx, "$argon2id$anything", []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify(cancelled) error = %v", err)
	}
}

func TestHasherSurfacesCSPRNGFailure(t *testing.T) {
	hasher, err := password.New(
		testPolicy(),
		password.WithRandom(failingReader{}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := hasher.Hash(context.Background(), []byte("secret")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Hash() error = %v, want wrapped io.ErrUnexpectedEOF", err)
	}
}

func TestVerifyReturnsNeedsRehashForWeakerArgon2id(t *testing.T) {
	oldPolicy := testPolicy()
	oldPolicy.MemoryKiB = 32
	oldPolicy.KeyBytes = 16
	oldHasher, err := password.New(oldPolicy)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := oldHasher.Hash(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	current, err := password.New(testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	verification, err := current.Verify(context.Background(), encoded, []byte("secret"))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !verification.Valid || !verification.NeedsRehash {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestVerifySupportsBoundedBcryptUpgrade(t *testing.T) {
	hasher, err := password.New(testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bcrypt.GenerateFromPassword([]byte("secret"), 12)
	if err != nil {
		t.Fatal(err)
	}

	verification, err := hasher.Verify(context.Background(), string(encoded), []byte("secret"))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !verification.Valid || !verification.NeedsRehash || verification.Algorithm != "bcrypt" {
		t.Fatalf("verification = %#v", verification)
	}
	mismatch, err := hasher.Verify(context.Background(), string(encoded), []byte("wrong"))
	if err != nil {
		t.Fatalf("Verify(wrong bcrypt) error = %v", err)
	}
	if mismatch.Valid || mismatch.Algorithm != "bcrypt" {
		t.Fatalf("mismatch = %#v", mismatch)
	}

	weak, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hasher.Verify(context.Background(), string(weak), []byte("secret")); !errors.Is(err, password.ErrHashPolicy) {
		t.Fatalf("Verify(weak bcrypt) error = %v, want ErrHashPolicy", err)
	}
	if _, err := hasher.Verify(context.Background(), "$2b$12$broken", []byte("secret")); !errors.Is(err, password.ErrMalformedHash) {
		t.Fatalf("Verify(malformed bcrypt) error = %v", err)
	}
	if _, err := hasher.Verify(context.Background(), string(encoded), []byte(strings.Repeat("x", 73))); !errors.Is(err, password.ErrHashPolicy) {
		t.Fatalf("Verify(long bcrypt password) error = %v", err)
	}
}

func TestVerifyRejectsUnsupportedAndOversizedHashes(t *testing.T) {
	policy := testPolicy()
	policy.MaxPHCBytes = 128
	hasher, err := password.New(policy)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := hasher.Verify(context.Background(), "$scrypt$not-supported", []byte("secret")); !errors.Is(err, password.ErrUnsupportedHash) {
		t.Fatalf("Verify(unsupported) error = %v", err)
	}
	if _, err := hasher.Verify(context.Background(), strings.Repeat("x", 129), []byte("secret")); !errors.Is(err, password.ErrHashPolicy) {
		t.Fatalf("Verify(oversized) error = %v", err)
	}
}

func TestHasherHonorsCancellationBeforeSemaphoreAdmission(t *testing.T) {
	reader := &blockingReader{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	policy := testPolicy()
	policy.MaxConcurrent = 1
	hasher, err := password.New(policy, password.WithRandom(reader))
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := hasher.Hash(context.Background(), []byte("first"))
		firstDone <- err
	}()
	<-reader.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hasher.Hash(ctx, []byte("second")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hash(saturated) error = %v, want context.Canceled", err)
	}

	close(reader.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Hash() error = %v", err)
	}
}

func TestNewRejectsInvalidPolicies(t *testing.T) {
	tests := []func(*password.Policy){
		func(policy *password.Policy) { policy.MemoryKiB = 0 },
		func(policy *password.Policy) { policy.MemoryKiB = 1024*1024 + 1 },
		func(policy *password.Policy) { policy.Iterations = 0 },
		func(policy *password.Policy) { policy.Iterations = 11 },
		func(policy *password.Policy) { policy.Parallelism = 0 },
		func(policy *password.Policy) { policy.Parallelism = 17 },
		func(policy *password.Policy) { policy.SaltBytes = 0 },
		func(policy *password.Policy) { policy.SaltBytes = 65 },
		func(policy *password.Policy) { policy.KeyBytes = 0 },
		func(policy *password.Policy) { policy.KeyBytes = 65 },
		func(policy *password.Policy) { policy.MaxPasswordBytes = 0 },
		func(policy *password.Policy) { policy.MaxPasswordBytes = 1<<20 + 1 },
		func(policy *password.Policy) { policy.MaxPHCBytes = 0 },
		func(policy *password.Policy) { policy.MaxPHCBytes = 4<<10 + 1 },
		func(policy *password.Policy) { policy.MaxConcurrent = 0 },
		func(policy *password.Policy) { policy.MaxConcurrent = 1025 },
	}
	for index, mutate := range tests {
		policy := testPolicy()
		mutate(&policy)
		if _, err := password.New(policy); !errors.Is(err, password.ErrInvalidPolicy) {
			t.Errorf("case %d: New() error = %v, want ErrInvalidPolicy", index, err)
		}
	}

	if _, err := password.New(testPolicy(), nil); !errors.Is(err, password.ErrInvalidPolicy) {
		t.Fatalf("New(nil option) error = %v", err)
	}
	if _, err := password.New(testPolicy(), password.WithRandom(nil)); !errors.Is(err, password.ErrInvalidPolicy) {
		t.Fatalf("New(nil random) error = %v", err)
	}
}

func testPolicy() password.Policy {
	return password.Policy{
		MemoryKiB:        64,
		Iterations:       1,
		Parallelism:      1,
		SaltBytes:        16,
		KeyBytes:         32,
		MaxPasswordBytes: 1024,
		MaxPHCBytes:      512,
		MaxConcurrent:    2,
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

type blockingReader struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (r *blockingReader) Read(buffer []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	for index := range buffer {
		buffer[index] = byte(index)
	}
	return len(buffer), nil
}
