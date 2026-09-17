package password

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePHCRejectsMalformedAndDuplicateParameters(t *testing.T) {
	policy := Policy{
		MemoryKiB:        64,
		Iterations:       2,
		Parallelism:      2,
		SaltBytes:        16,
		KeyBytes:         32,
		MaxPasswordBytes: 1024,
		MaxPHCBytes:      512,
		MaxConcurrent:    1,
	}
	validSalt := "MDEyMzQ1Njc4OWFiY2RlZg"
	validKey := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"

	tests := []string{
		"",
		"$argon2id$v=18$m=64,t=1,p=1$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=64,m=64,t=1,p=1$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=64,t=1,p=1,x=2$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=65,t=1,p=1$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=64,t=3,p=1$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=64,t=1,p=3$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=64,t=1,p=1$%%%$" + validKey,
		"$argon2id$v=19$m=64,t=1,p=1$" + validSalt + "$%%%",
		"$argon2id$v=19$m=64,t=1,p=1$" + validSalt + "$" + strings.Repeat("A", 100),
		"$argon2id$v=19$m=64,t=1,p=1$YQ$" + validKey,
		"$argon2id$v=19$m=64,t=1,p=1$" + validSalt + "$YQ",
		"$argon2id$v=19$m=7,t=1,p=1$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=not-a-number,t=1,p=1$" + validSalt + "$" + validKey,
		"$argon2id$v=19$m=4294967296,t=1,p=1$" + validSalt + "$" + validKey,
	}

	for _, encoded := range tests {
		if _, err := parsePHC(encoded, policy); err == nil {
			t.Errorf("parsePHC(%q) error = nil", encoded)
		}
	}
}

func TestParsePHCClassifiesPolicyLimit(t *testing.T) {
	policy := DefaultPolicy()
	_, err := parsePHC(
		"$argon2id$v=19$m=1048577,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
		policy,
	)
	if !errors.Is(err, ErrHashPolicy) {
		t.Fatalf("parsePHC() error = %v, want ErrHashPolicy", err)
	}
}
