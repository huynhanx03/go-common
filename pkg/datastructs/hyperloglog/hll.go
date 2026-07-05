package hyperloglog

import (
	"math"
	"math/bits"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

const (
	// Number of registers (m = 2^p)
	p = 14
	m = 1 << p
	// Mask for register index
	mMask = m - 1
	// alphaMM is a constant used in the estimation formula
	alphaMM = 0.7213 / (1 + 1.079/m) * float64(m) * float64(m)
)

// HLL represents a HyperLogLog data structure for cardinality estimation.
// It uses 16384 registers (14 bits for index), providing ~0.81% error rate.
type HLL struct {
	registers []uint8
}

// New creates a new HyperLogLog instance.
func New() *HLL {
	return &HLL{
		registers: make([]uint8, m),
	}
}

// Add adds a hashed value to the HyperLogLog.
func (h *HLL) Add(hash uint64) bool {
	// Index is the first p bits
	idx := hash & mMask
	// Value is the remaining bits, count leading zeros + 1
	// We shift by p to ignore index bits
	w := hash >> p
	// rho is the position of the leftmost 1-bit in the remaining 64-p bits.
	// Shift w back to occupy the high bits so LeadingZeros64 works correctly.
	rho := uint8(bits.LeadingZeros64(w<<p)) + 1

	if rho > h.registers[idx] {
		h.registers[idx] = rho
		return true
	}
	return false
}

// Count returns the estimated cardinality.
func (h *HLL) Count() int64 {
	sum := 0.0
	zeros := 0
	for _, val := range h.registers {
		sum += 1.0 / float64(uint64(1)<<val)
		if val == 0 {
			zeros++
		}
	}

	estimate := alphaMM / sum

	// Linear Counting for small ranges
	if estimate <= 2.5*float64(m) {
		if zeros > 0 {
			estimate = float64(m) * math.Log(float64(m)/float64(zeros))
		}
		// Large range correction
	} else if estimate > (1.0/30.0)*math.Pow(2.0, 64.0) {
		estimate = -math.Pow(2.0, 64.0) * math.Log(1.0-estimate/math.Pow(2.0, 64.0))
	}

	return int64(estimate)
}

// Merge merges another HyperLogLog into this one.
func (h *HLL) Merge(other *HLL) {
	if len(other.registers) != m {
		return
	}
	for i := 0; i < m; i++ {
		if other.registers[i] > h.registers[i] {
			h.registers[i] = other.registers[i]
		}
	}
}

// MarshalJSON implements json.Marshaler.
func (h *HLL) MarshalJSON() ([]byte, error) {
	return json.Marshal(h.registers)
}

// UnmarshalJSON implements json.Unmarshaler.
func (h *HLL) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &h.registers)
}

// Clone returns a deep copy of the HyperLogLog.
func (h *HLL) Clone() *HLL {
	newRegs := make([]uint8, m)
	copy(newRegs, h.registers)
	return &HLL{registers: newRegs}
}
