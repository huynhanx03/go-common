package cuckoo

import (
	"errors"
	"math/rand"
	"time"

	"github.com/huynhanx03/go-common/pkg/hash"
)

const (
	// bucketSize is the number of fingerprints per bucket.
	bucketSize = 4
	// maxKicks is the maximum number of relocations during cuckoo insertion.
	maxKicks = 500
)

// bucket is a fixed-size array of fingerprints with an explicit length.
// Using a fixed array avoids a heap allocation per bucket.
type bucket struct {
	fps [bucketSize]uint16
	len uint8
}

// add appends a fingerprint to the bucket. Returns false if full.
func (b *bucket) add(fp uint16) bool {
	if int(b.len) >= bucketSize {
		return false
	}
	b.fps[b.len] = fp
	b.len++
	return true
}

// contains returns true if the bucket holds the given fingerprint.
func (b *bucket) contains(fp uint16) bool {
	for i := uint8(0); i < b.len; i++ {
		if b.fps[i] == fp {
			return true
		}
	}
	return false
}

// remove deletes the first occurrence of fp. Returns true if found.
func (b *bucket) remove(fp uint16) bool {
	for i := uint8(0); i < b.len; i++ {
		if b.fps[i] == fp {
			b.len--
			b.fps[i] = b.fps[b.len]
			b.fps[b.len] = 0
			return true
		}
	}
	return false
}

// swap replaces the fingerprint at idx and returns the old value.
func (b *bucket) swap(idx int, fp uint16) uint16 {
	old := b.fps[idx]
	b.fps[idx] = fp
	return old
}

// Filter represents a Cuckoo Filter.
type Filter struct {
	buckets []bucket
	count   uint
	m       uint // number of buckets
	rnd     *rand.Rand
}

// New creates a new Cuckoo Filter with the given capacity.
// Actual number of buckets will be rounded up to a power of two.
func New(capacity uint) *Filter {
	m := uint(1)
	for m < capacity/bucketSize {
		m <<= 1
	}

	return &Filter{
		buckets: make([]bucket, m),
		m:       m,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// fingerprint derives a non-zero 16-bit fingerprint from a hash value.
func fingerprint(h uint64) uint16 {
	f := uint16(h % 65535)
	if f == 0 {
		f = 1
	}
	return f
}

// indices computes the two candidate bucket indices for a given hash and fingerprint.
func (f *Filter) indices(h uint64, finger uint16) (uint, uint) {
	i1 := uint(h % uint64(f.m))
	_, hf := hash.KeyToHash(uint64(finger))
	i2 := i1 ^ uint(hf%uint64(f.m))
	return i1, i2
}

// Add adds an item to the filter.
func (f *Filter) Add(item string) error {
	h1, h2 := hash.KeyToHash(item)
	fp := fingerprint(h2)
	i1, i2 := f.indices(h1, fp)

	if f.buckets[i1].add(fp) {
		f.count++
		return nil
	}

	if f.buckets[i2].add(fp) {
		f.count++
		return nil
	}

	// Both buckets full — start cuckoo kicks.
	i := i1
	if f.rnd.Intn(2) == 0 {
		i = i2
	}

	for k := 0; k < maxKicks; k++ {
		fp = f.buckets[i].swap(f.rnd.Intn(bucketSize), fp)

		_, hf := hash.KeyToHash(uint64(fp))
		i = i ^ uint(hf%uint64(f.m))

		if f.buckets[i].add(fp) {
			f.count++
			return nil
		}
	}

	return errors.New("filter full")
}

// Contains checks if the filter probably contains the item.
func (f *Filter) Contains(item string) bool {
	h1, h2 := hash.KeyToHash(item)
	fp := fingerprint(h2)
	i1, i2 := f.indices(h1, fp)
	return f.buckets[i1].contains(fp) || f.buckets[i2].contains(fp)
}

// Delete removes an item from the filter.
func (f *Filter) Delete(item string) bool {
	h1, h2 := hash.KeyToHash(item)
	fp := fingerprint(h2)
	i1, i2 := f.indices(h1, fp)

	if f.buckets[i1].remove(fp) {
		f.count--
		return true
	}
	if f.buckets[i2].remove(fp) {
		f.count--
		return true
	}
	return false
}

// Count returns the number of items in the filter.
func (f *Filter) Count() uint {
	return f.count
}
