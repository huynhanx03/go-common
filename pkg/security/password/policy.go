package password

import (
	"fmt"

	"github.com/huynhanx03/go-common/pkg/settings"
)

type Policy struct {
	MemoryKiB        uint32
	Iterations       uint32
	Parallelism      uint8
	SaltBytes        uint32
	KeyBytes         uint32
	MaxPasswordBytes int
	MaxPHCBytes      int
	MaxConcurrent    int
}

func DefaultPolicy() Policy {
	return Policy{
		MemoryKiB:        defaultMemoryKiB,
		Iterations:       defaultIterations,
		Parallelism:      defaultParallelism,
		SaltBytes:        defaultSaltBytes,
		KeyBytes:         defaultKeyBytes,
		MaxPasswordBytes: defaultMaxPasswordBytes,
		MaxPHCBytes:      defaultMaxPHCBytes,
		MaxConcurrent:    defaultMaxConcurrent,
	}
}

func DefaultPassword() settings.Password {
	policy := DefaultPolicy()
	return settings.Password{
		MemoryKiB:     policy.MemoryKiB,
		Iterations:    policy.Iterations,
		Parallelism:   policy.Parallelism,
		MaxConcurrent: policy.MaxConcurrent,
	}
}

func (p Policy) validate() error {
	switch {
	case p.MemoryKiB < uint32(8)*uint32(p.Parallelism):
		return invalidPolicy("memory must be at least 8 KiB per parallel lane")
	case p.MemoryKiB > hardMaxMemoryKiB:
		return invalidPolicy("memory exceeds hard limit")
	case p.Iterations == 0 || p.Iterations > hardMaxIterations:
		return invalidPolicy("iterations are outside the supported range")
	case p.Parallelism == 0 || p.Parallelism > hardMaxParallelism:
		return invalidPolicy("parallelism is outside the supported range")
	case p.SaltBytes < 8 || p.SaltBytes > hardMaxSaltBytes:
		return invalidPolicy("salt length is outside the supported range")
	case p.KeyBytes < 16 || p.KeyBytes > hardMaxKeyBytes:
		return invalidPolicy("key length is outside the supported range")
	case p.MaxPasswordBytes <= 0 || p.MaxPasswordBytes > hardMaxPasswordBytes:
		return invalidPolicy("maximum password length is outside the supported range")
	case p.MaxPHCBytes < 64 || p.MaxPHCBytes > hardMaxPHCBytes:
		return invalidPolicy("maximum PHC length is outside the supported range")
	case p.MaxConcurrent <= 0 || p.MaxConcurrent > hardMaxConcurrent:
		return invalidPolicy("maximum concurrency is outside the supported range")
	default:
		return nil
	}
}

func invalidPolicy(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPolicy, message)
}
