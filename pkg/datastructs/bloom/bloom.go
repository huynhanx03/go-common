package bloom

import (
	"encoding/json"
	"errors"
	"math"
	"sync"

	"github.com/huynhanx03/go-common/pkg/common/locks"
	"github.com/huynhanx03/go-common/pkg/hash"
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
	lock   sync.Locker
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
		lock:   locks.NewSpinLock(),
	}, nil
}

// Add adds a hashed key to the bloom filter.
func (b *Bloom) Add(hash uint64) {
	b.lock.Lock()
	defer b.lock.Unlock()

	h := hash
	delta := (h >> 17) | (h << 47) // Rotate to get a different mix
	for i := uint64(0); i < b.k; i++ {
		idx := (h + i*delta) % b.m
		b.bitset[idx/64] |= (1 << (idx % 64))
	}
}

// AddString adds a string key to the bloom filter.
func (b *Bloom) AddString(key string) {
	_, h := hash.KeyToHash(key)
	b.Add(h)
}

// AddIfNotHas checks if the key exists and adds it if not.
// Returns true if the key was already present, false otherwise.
func (b *Bloom) AddIfNotHas(hash uint64) bool {
	b.lock.Lock()
	defer b.lock.Unlock()

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
	b.lock.Lock()
	defer b.lock.Unlock()

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

// HasString checks if the string key is present in the bloom filter.
func (b *Bloom) HasString(key string) bool {
	_, h := hash.KeyToHash(key)
	return b.Has(h)
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
	b.lock = locks.NewSpinLock()
	return nil
}

// TotalSize returns the total size of the bloom filter in bits.
func (b *Bloom) TotalSize() uint64 {
	return b.m
}
