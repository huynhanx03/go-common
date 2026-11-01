package password

import (
	"errors"
	"testing"
)

func TestDefaultPolicyContainsCurrentPasswordDefaults(t *testing.T) {
	t.Parallel()

	policy := DefaultPolicy()
	if policy.MemoryKiB != defaultMemoryKiB || policy.Iterations != defaultIterations ||
		policy.Parallelism != defaultParallelism || policy.SaltBytes != defaultSaltBytes ||
		policy.KeyBytes != defaultKeyBytes || policy.MaxPasswordBytes != defaultMaxPasswordBytes ||
		policy.MaxPHCBytes != defaultMaxPHCBytes || policy.MaxConcurrent != defaultMaxConcurrent {
		t.Fatalf("DefaultPolicy() = %+v, want package defaults", policy)
	}
	if err := policy.validate(); err != nil {
		t.Fatalf("DefaultPolicy().validate() error = %v", err)
	}

	maximum := Policy{
		MemoryKiB:        hardMaxMemoryKiB,
		Iterations:       hardMaxIterations,
		Parallelism:      hardMaxParallelism,
		SaltBytes:        hardMaxSaltBytes,
		KeyBytes:         hardMaxKeyBytes,
		MaxPasswordBytes: hardMaxPasswordBytes,
		MaxPHCBytes:      hardMaxPHCBytes,
		MaxConcurrent:    hardMaxConcurrent,
	}
	if err := maximum.validate(); err != nil {
		t.Fatalf("maximum supported policy rejected: %v", err)
	}
}

func TestPolicyValidateRejectsUnsupportedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*Policy)
	}{
		{name: "memory below lane minimum", change: func(p *Policy) { p.MemoryKiB = 15 }},
		{name: "memory above hard limit", change: func(p *Policy) { p.MemoryKiB = hardMaxMemoryKiB + 1 }},
		{name: "zero iterations", change: func(p *Policy) { p.Iterations = 0 }},
		{name: "iterations above hard limit", change: func(p *Policy) { p.Iterations = hardMaxIterations + 1 }},
		{name: "zero parallelism", change: func(p *Policy) { p.Parallelism = 0 }},
		{name: "parallelism above hard limit", change: func(p *Policy) { p.Parallelism = hardMaxParallelism + 1 }},
		{name: "salt too short", change: func(p *Policy) { p.SaltBytes = 7 }},
		{name: "salt above hard limit", change: func(p *Policy) { p.SaltBytes = hardMaxSaltBytes + 1 }},
		{name: "key too short", change: func(p *Policy) { p.KeyBytes = 15 }},
		{name: "key above hard limit", change: func(p *Policy) { p.KeyBytes = hardMaxKeyBytes + 1 }},
		{name: "password maximum zero", change: func(p *Policy) { p.MaxPasswordBytes = 0 }},
		{name: "password maximum above hard limit", change: func(p *Policy) { p.MaxPasswordBytes = hardMaxPasswordBytes + 1 }},
		{name: "PHC maximum too small", change: func(p *Policy) { p.MaxPHCBytes = 63 }},
		{name: "PHC maximum above hard limit", change: func(p *Policy) { p.MaxPHCBytes = hardMaxPHCBytes + 1 }},
		{name: "concurrency zero", change: func(p *Policy) { p.MaxConcurrent = 0 }},
		{name: "concurrency above hard limit", change: func(p *Policy) { p.MaxConcurrent = hardMaxConcurrent + 1 }},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			policy := DefaultPolicy()
			test.change(&policy)
			if err := policy.validate(); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("validate() error = %v, want ErrInvalidPolicy", err)
			}
		})
	}
}
