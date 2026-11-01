package password

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestNewUsesPasswordSettingsAndKeepsInternalDefaults(t *testing.T) {
	t.Parallel()

	config := DefaultPassword()
	config.MemoryKiB = 32
	config.Iterations = 1
	config.Parallelism = 1
	config.MaxConcurrent = 3

	hasher, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := hasher.policy; got.MemoryKiB != config.MemoryKiB ||
		got.Iterations != config.Iterations ||
		got.Parallelism != config.Parallelism ||
		got.MaxConcurrent != config.MaxConcurrent {
		t.Fatalf("New() policy = %+v, doesn't reflect settings %+v", got, config)
	}
	defaults := DefaultPolicy()
	if hasher.policy.SaltBytes != defaults.SaltBytes ||
		hasher.policy.KeyBytes != defaults.KeyBytes ||
		hasher.policy.MaxPasswordBytes != defaults.MaxPasswordBytes ||
		hasher.policy.MaxPHCBytes != defaults.MaxPHCBytes {
		t.Fatalf("New() changed fixed policy defaults: %+v", hasher.policy)
	}

	encoded, err := hasher.Hash(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	result, err := hasher.Verify(context.Background(), encoded, []byte("secret"))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !result.Valid || result.NeedsRehash {
		t.Fatalf("Verify() = %+v, want valid hash with no rehash", result)
	}
}

func TestDefaultPasswordMatchesDefaultPolicy(t *testing.T) {
	t.Parallel()

	config := DefaultPassword()
	policy := DefaultPolicy()
	if config.MemoryKiB != policy.MemoryKiB || config.Iterations != policy.Iterations ||
		config.Parallelism != policy.Parallelism || config.MaxConcurrent != policy.MaxConcurrent {
		t.Fatalf("DefaultPassword() = %+v, want defaults from DefaultPolicy()", config)
	}
}

func TestNewRejectsInvalidPasswordSettings(t *testing.T) {
	t.Parallel()

	config := DefaultPassword()
	config.Iterations = 0
	if _, err := New(config); err == nil {
		t.Fatal("New() accepted invalid password settings")
	}
}

func TestNewWithPolicyValidatesAndAppliesOptions(t *testing.T) {
	t.Parallel()

	policy := DefaultPolicy()
	if _, err := NewWithPolicy(policy, nil); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewWithPolicy() nil option error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := NewWithPolicy(Policy{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewWithPolicy() invalid policy error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := NewWithPolicy(policy, WithRandom(nil)); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewWithPolicy() nil random error = %v, want ErrInvalidPolicy", err)
	}
	optionErr := errors.New("option failed")
	if _, err := NewWithPolicy(policy, func(*options) error { return optionErr }); !errors.Is(err, optionErr) {
		t.Fatalf("NewWithPolicy() option error = %v, want wrapped option error", err)
	}

	random := bytes.NewReader(make([]byte, policy.SaltBytes))
	hasher, err := NewWithPolicy(testPolicy(32, 1, 1), WithRandom(random))
	if err != nil {
		t.Fatalf("NewWithPolicy() error = %v", err)
	}
	if _, err := hasher.Hash(context.Background(), []byte("secret")); err != nil {
		t.Fatalf("Hash() with injected random source error = %v", err)
	}
}
