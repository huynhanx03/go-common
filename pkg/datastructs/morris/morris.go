package morris

import (
	"math/rand"
	"time"
)

// Morris represents a Morris Counter.
// It uses a single uint8 to estimate counts up to 2^255 (though limited by float64 precision).
type Morris struct {
	value uint8
	rnd   *rand.Rand
}

// New creates a new Morris Counter.
func New() *Morris {
	return &Morris{
		value: 0,
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Increment increments the counter probabilistically.
// Probability of incrementing is 1 / 2^value.
func (m *Morris) Increment() bool {
	p := 1.0 / float64(uint64(1)<<m.value)
	if m.rnd.Float64() < p {
		m.value++
		return true
	}
	return false
}

// Count returns the estimated count.
// Estimate = 2^value - 1.
func (m *Morris) Count() uint64 {
	if m.value == 0 {
		return 0
	}
	return (1 << m.value) - 1
}

// Reset resets the counter.
func (m *Morris) Reset() {
	m.value = 0
}

// Value returns the raw register value.
func (m *Morris) RawValue() uint8 {
	return m.value
}

// SetRawValue sets the raw register value (e.g. for loading from storage).
func (m *Morris) SetRawValue(v uint8) {
	m.value = v
}
