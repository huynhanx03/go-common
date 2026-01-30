package hash

import (
	"github.com/huynhanx03/go-common/pkg/runtime"

	"github.com/cespare/xxhash/v2"
)

type Key interface {
	uint64 | string | []byte | byte | int | uint | int32 | uint32 | int64
}

// KeyToHash generates a 128-bit hash (as two uint64s) for a given key.
// It uses runtime.MemHash for the first 64 bits (fast, process-specific seed)
// and xxhash for the second 64 bits (high quality, stable).
func KeyToHash[K Key](key K) (uint64, uint64) {
	keyAsAny := any(key)
	switch k := keyAsAny.(type) {
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
		panic("Key type not supported")
	}
}
