package bloom

import (
	"encoding/json"
	"errors"
	"math"
)

const (
	// ln2 is the natural logarithm of 2
	ln2 = 0.69314718056
	// ln2sq is ln(2)^2
	ln2sq = 0.48045301391
)

// Bloom represents a probabilistic set of data.
type Bloom struct {
	bitset []uint64
	k      uint64 // Number of hash functions
	m      uint64 // Size of bitset in bits
}

// New creates a new Bloom filter.
// capacity: estimate of the number of elements to add.
// fpRate: desired false positive rate (0 < fpRate < 1).
func New(capacity uint64, fpRate float64) (*Bloom, error) {
	if capacity == 0 {
		return nil, errors.New("capacity must be greater than 0")
	}
	if fpRate <= 0 || fpRate >= 1 {
		return nil, errors.New("fpRate must be between 0 and 1")
	}

	// m = -n * ln(p) / (ln(2)^2)
	size := -float64(capacity) * math.Log(fpRate) / ln2sq
	m := uint64(math.Ceil(size))

	// k = (m / n) * ln(2)
	kFloat := (float64(m) / float64(capacity)) * ln2
	k := uint64(math.Ceil(kFloat))

	return &Bloom{
		bitset: make([]uint64, (m+63)/64),
		k:      k,
		m:      m,
	}, nil
}

// Add adds a hashed key to the bloom filter.
func (b *Bloom) Add(hash uint64) {
	h := hash
	delta := (h >> 17) | (h << 47) // Rotate to get a different mix
	for i := uint64(0); i < b.k; i++ {
		idx := (h + i*delta) % b.m
		b.bitset[idx/64] |= (1 << (idx % 64))
	}
}

// AddIfNotHas checks if the key exists and adds it if not.
// Returns true if the key was already present, false otherwise.
func (b *Bloom) AddIfNotHas(hash uint64) bool {
	h := hash
	delta := (h >> 17) | (h << 47)
	present := true
	for i := uint64(0); i < b.k; i++ {
		idx := (h + i*delta) % b.m
		bitIdx := idx / 64
		mask := uint64(1) << (idx % 64)

		if (b.bitset[bitIdx] & mask) == 0 {
			present = false
			b.bitset[bitIdx] |= mask
		}
	}
	return present
}

// Has checks if the hash is present in the bloom filter.
func (b *Bloom) Has(hash uint64) bool {
	h := hash
	delta := (h >> 17) | (h << 47)
	for i := uint64(0); i < b.k; i++ {
		idx := (h + i*delta) % b.m
		if (b.bitset[idx/64] & (1 << (idx % 64))) == 0 {
			return false
		}
	}
	return true
}

// Clear resets the Bloom filter.
func (b *Bloom) Clear() {
	for i := range b.bitset {
		b.bitset[i] = 0
	}
}

// bloomJSON is a helper for JSON marshaling.
type bloomJSON struct {
	Bitset []uint64 `json:"bitset"`
	K      uint64   `json:"k"`
	M      uint64   `json:"m"`
}

// MarshalJSON implements json.Marshaler.
func (b *Bloom) MarshalJSON() ([]byte, error) {
	return json.Marshal(bloomJSON{
		Bitset: b.bitset,
		K:      b.k,
		M:      b.m,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *Bloom) UnmarshalJSON(data []byte) error {
	var temp bloomJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}
	b.bitset = temp.Bitset
	b.k = temp.K
	b.m = temp.M
	return nil
}

// TotalSize returns the total size of the bloom filter in bits.
func (b *Bloom) TotalSize() uint64 {
	return b.m
}
