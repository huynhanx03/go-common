package tinylfu

import (
	"github.com/huynhanx03/go-common/pkg/datastructs/bloom"
	"github.com/huynhanx03/go-common/pkg/datastructs/sketch"
)

// Frequency implements TinyLFU frequency counting.
// It combines a Count-Min Sketch for frequency estimation and a Bloom filter
// as a "doorkeeper" to filter first-time accesses.
type Frequency struct {
	freq       *sketch.Sketch
	door       *bloom.Bloom
	incr       int64
	resetAfter int64
}

// NewFrequency creates a new frequency counter.
func NewFrequency(numCounters int64) *Frequency {
	door, _ := bloom.New(uint64(numCounters), 0.01)
	return &Frequency{
		freq:       sketch.New(numCounters),
		door:       door,
		resetAfter: numCounters,
	}
}

// Record records an access to the given key.
func (f *Frequency) Record(key uint64) {
	f.incr++
	if f.incr >= f.resetAfter {
		f.freq.Reset()
		f.door.Clear()
		f.incr = 0
	}

	if f.door.AddIfNotHas(key) {
		f.freq.Increment(key)
	}
}

// Estimate returns the estimated access frequency of a key.
func (f *Frequency) Estimate(key uint64) int64 {
	hits := f.freq.Estimate(key)
	if f.door.Has(key) {
		hits++
	}
	return hits
}

// Clear resets all frequency data.
func (f *Frequency) Clear() {
	f.freq.Clear()
	f.door.Clear()
	f.incr = 0
}
