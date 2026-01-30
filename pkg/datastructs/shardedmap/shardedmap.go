package shardedmap

import (
	"sync"

	"github.com/huynhanx03/go-common/pkg/utils"
)

// Map is a thread-safe map that uses sharding to minimize lock contention.
// It supports any comparable key type K and any value type V.
type Map[K comparable, V any] struct {
	shards []*lockedShard[K, V]
	mask   uint64
	hasher func(K) uint64
}

type lockedShard[K comparable, V any] struct {
	sync.RWMutex
	data map[K]V

	// Padding prevents false sharing by ensuring each shard struct is large enough
	// to occupy its own cache line (typically 64 bytes).
	// RWMutex (24) + Map (8) = 32 bytes.
	// We add 40 bytes padding to reach > 64 bytes, ensuring independent allocation blocks.
	// Using [64]byte is simpler and safer to guarantee separation.
	pad [64]byte
}

// New creates a new Sharded Map.
// shards: Number of shards to use. Will be rounded up to the nearest power of 2.
// hashFn: Function to hash the key K into a uint64.
func New[K comparable, V any](shards int, hashFn func(K) uint64) *Map[K, V] {
	if shards <= 0 {
		shards = 256 // Default reasonable value
	}
	numShards := utils.CeilToPowerOfTwo(shards)
	m := &Map[K, V]{
		shards: make([]*lockedShard[K, V], numShards),
		mask:   uint64(numShards - 1),
		hasher: hashFn,
	}

	for i := range m.shards {
		m.shards[i] = &lockedShard[K, V]{
			data: make(map[K]V),
		}
	}
	return m
}

// Get retrieves a value from the map.
func (m *Map[K, V]) Get(key K) (V, bool) {
	hash := m.hasher(key)
	shard := m.shards[hash&m.mask]

	shard.RLock()
	val, ok := shard.data[key]
	shard.RUnlock()
	return val, ok
}

// Set adds or updates a value in the map.
func (m *Map[K, V]) Set(key K, value V) {
	hash := m.hasher(key)
	shard := m.shards[hash&m.mask]

	shard.Lock()
	shard.data[key] = value
	shard.Unlock()
}

// Del removes a value from the map.
func (m *Map[K, V]) Del(key K) {
	hash := m.hasher(key)
	shard := m.shards[hash&m.mask]

	shard.Lock()
	delete(shard.data, key)
	shard.Unlock()
}

// Len returns the total number of items in the map.
// Note: This iterates over all shards and locks them individually, so it's not atomic across the whole map.
func (m *Map[K, V]) Len() int {
	total := 0
	for _, shard := range m.shards {
		shard.RLock()
		total += len(shard.data)
		shard.RUnlock()
	}
	return total
}

// Clear removes all items from the map.
func (m *Map[K, V]) Clear() {
	for _, shard := range m.shards {
		shard.Lock()
		shard.data = make(map[K]V)
		shard.Unlock()
	}
}

// Do iterates over all items in the map and executes fn.
// It locks one shard at a time.
func (m *Map[K, V]) Do(fn func(K, V)) {
	for _, shard := range m.shards {
		shard.RLock()
		for k, v := range shard.data {
			fn(k, v)
		}
		shard.RUnlock()
	}
}
