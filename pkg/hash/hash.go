package hash

import (
	"fmt"
	"strconv"

	"github.com/huynhanx03/go-common/pkg/runtime"

	"github.com/cespare/xxhash/v2"
)

type Key interface {
	uint64 | string | byte | []byte | uint | int | int32 | uint32 | int64
}

// KeyToHash generates a 128-bit hash (as two uint64s) for a given key.
// It uses runtime.MemHash for the first 64 bits (fast, process-specific seed)
// and xxhash for the second 64 bits (high quality, stable).
func KeyToHash(key any) (uint64, uint64) {
	switch k := key.(type) {
	case uint64:
		return k, 0
	case string:
		return runtime.MemHashString(k), xxhash.Sum64String(k)
	case []byte:
		return runtime.MemHash(k), xxhash.Sum64(k)
	case byte:
		return uint64(k), 0
	case uint:
		return uint64(k), 0
	case int:
		return uint64(k), 0
	case int32:
		return uint64(k), 0
	case uint32:
		return uint64(k), 0
	case int64:
		return uint64(k), 0
	default:
		// For unsupported types, use a slow path or generic representation
		s := ToString(key)
		return runtime.MemHashString(s), xxhash.Sum64String(s)
	}
}


// Sum64 returns the 64-bit hash of a key.
func Sum64[K Key](key K) uint64 {
	h, _ := KeyToHash(key)
	return h
}

// Hash64WithSeed returns a 64-bit hash of a key using a specific seed.
func Hash64WithSeed[K Key](key K, seed uint64) uint64 {
	keyAsAny := any(key)
	switch k := keyAsAny.(type) {
	case string:
		h := xxhash.NewWithSeed(seed)
		_, _ = h.WriteString(k)
		return h.Sum64()
	case []byte:
		h := xxhash.NewWithSeed(seed)
		_, _ = h.Write(k)
		return h.Sum64()
	default:
		h1, h2 := KeyToHash(key)
		return h1 ^ h2 ^ seed
	}
}

// ToString converts a generic key to string.
func ToString[K any](key K) string {
	switch k := any(key).(type) {
	case string:
		return k
	case []byte:
		return string(k)
	case uint64:
		return strconv.FormatUint(k, 10)
	case int:
		return strconv.Itoa(k)
	case byte:
		return string([]byte{k})
	default:
		return fmt.Sprint(key)
	}
}
