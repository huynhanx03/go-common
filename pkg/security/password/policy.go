package password

import (
	"fmt"
	"math"
)

const (
	hardMaxMemoryKiB     = uint32(1024 * 1024)
	hardMaxIterations    = uint32(10)
	hardMaxParallelism   = uint8(16)
	hardMaxSaltBytes     = uint32(64)
	hardMaxKeyBytes      = uint32(64)
	hardMaxPasswordBytes = 1 << 20
	hardMaxPHCBytes      = 4 << 10
	hardMaxConcurrent    = 1024
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
		MemoryKiB:        64 * 1024,
		Iterations:       3,
		Parallelism:      2,
		SaltBytes:        16,
		KeyBytes:         32,
		MaxPasswordBytes: 1024,
		MaxPHCBytes:      512,
		MaxConcurrent:    2,
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
	case uint64(p.MemoryKiB)*1024 > uint64(math.MaxInt):
		return invalidPolicy("memory size overflows this platform")
	case uint64(p.SaltBytes) > uint64(math.MaxInt),
		uint64(p.KeyBytes) > uint64(math.MaxInt):
		return invalidPolicy("encoded length overflows this platform")
	default:
		return nil
	}
}

func invalidPolicy(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPolicy, message)
}
