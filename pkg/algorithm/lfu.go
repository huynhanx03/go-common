package algorithm

import "math/rand"

const defaultLFUSampleSize = 5

// LFUEntry holds a cache key and its 8-bit logarithmic access counter.
type LFUEntry struct {
	Key     uint64
	Counter uint8
}

// SelectLFUVictim returns the least frequently used entry from a random sample.
// Based on Redis approximated LFU (evict.c): randomly sample a few keys and
// evict the one with the lowest access counter.
// Returns zero-value and false when the pool is empty.
func SelectLFUVictim(pool []LFUEntry, sampleSize int) (LFUEntry, bool) {
	n := len(pool)
	if n == 0 {
		return LFUEntry{}, false
	}

	if sampleSize <= 0 {
		sampleSize = defaultLFUSampleSize
	}

	if sampleSize >= n {
		return findLeastFrequent(pool), true
	}

	sample := make([]LFUEntry, sampleSize)
	for i := 0; i < sampleSize; i++ {
		sample[i] = pool[rand.Intn(n)]
	}

	return findLeastFrequent(sample), true
}

// findLeastFrequent returns the entry with the smallest Counter.
func findLeastFrequent(entries []LFUEntry) LFUEntry {
	least := entries[0]
	for i := 1; i < len(entries); i++ {
		if entries[i].Counter < least.Counter {
			least = entries[i]
		}
	}
	return least
}
