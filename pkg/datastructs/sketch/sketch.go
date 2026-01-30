package sketch

import (
	"math/rand"
	"time"

	"github.com/huynhanx03/go-common/pkg/utils"
)

// Sketch is a Count-Min sketch implementation with 4-bit counters.
// NOT thread-safe.
type Sketch struct {
	rows [cmDepth]cmRow
	seed [cmDepth]uint64
	mask uint64
}

// New creates a new Count-Min sketch.
func New(numCounters int64) *Sketch {
	if numCounters <= 0 {
		numCounters = 1
	}
	n := utils.CeilToPowerOfTwo(int(numCounters))
	s := &Sketch{
		mask: uint64(n - 1),
	}

	source := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < cmDepth; i++ {
		s.seed[i] = source.Uint64()
		s.rows[i] = newCmRow(int64(n))
	}
	return s
}

// Increment increments the counter for the given hash.
func (s *Sketch) Increment(hash uint64) {
	for i := range s.rows {
		idx := (hash ^ s.seed[i]) & s.mask
		s.rows[i].increment(idx)
	}
}

// Estimate returns the estimated frequency of the given hash.
func (s *Sketch) Estimate(hash uint64) int64 {
	min := byte(255)
	for i := range s.rows {
		idx := (hash ^ s.seed[i]) & s.mask
		val := s.rows[i].get(idx)
		if val < min {
			min = val
		}
	}
	return int64(min)
}

// Reset halves all counter values.
func (s *Sketch) Reset() {
	for _, r := range s.rows {
		r.reset()
	}
}

// Clear zeroes all counters.
func (s *Sketch) Clear() {
	for _, r := range s.rows {
		r.clear()
	}
}
