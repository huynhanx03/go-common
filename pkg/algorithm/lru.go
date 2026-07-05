package algorithm

import "math/rand"

const defaultLRUSampleSize = 5

// LRUEntry holds a cache key and its last-access timestamp (unix nano).
type LRUEntry struct {
	Key        uint64
	LastAccess int64
}

// SelectLRUVictim returns the least recently used entry from a random sample.
// Based on Redis approximated LRU (evict.c): sample random keys and evict the
// oldest one, avoiding the cost of a true LRU linked list.
// Returns zero-value and false when the pool is empty.
func SelectLRUVictim(pool []LRUEntry, sampleSize int) (LRUEntry, bool) {
	n := len(pool)
	if n == 0 {
		return LRUEntry{}, false
	}

	if sampleSize <= 0 {
		sampleSize = defaultLRUSampleSize
	}

	if sampleSize >= n {
		return findOldest(pool), true
	}


	sample := make([]LRUEntry, sampleSize)
	for i := 0; i < sampleSize; i++ {
		sample[i] = pool[rand.Intn(n)]
	}

	return findOldest(sample), true
}

// findOldest returns the entry with the smallest LastAccess from the slice.
func findOldest(entries []LRUEntry) LRUEntry {
	oldest := entries[0]
	for i := 1; i < len(entries); i++ {
		if entries[i].LastAccess < oldest.LastAccess {
			oldest = entries[i]
		}
	}
	return oldest
}
