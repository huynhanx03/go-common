package hash

import (
	"encoding/binary"

	"github.com/cespare/xxhash/v2"
)

// secondarySeed creates the independent second member of a deterministic
// double hash. It is an algorithm constant, not a secret.
const secondarySeed uint64 = 0x9e3779b97f4a7c15

// Key is the set of values with a canonical byte encoding supported by this
// package. Hashing is deterministic for these built-in types across processes.
type Key interface {
	uint64 | string | byte | []byte | uint | int | int32 | uint32 | int64
}

// HashPair is a deterministic pair of 64-bit hashes for one Key. It is useful
// for double-hashing data structures such as a Cuckoo filter. HashPair is not
// a cryptographic digest.
type HashPair struct {
	// Primary is the unseeded xxhash64 result.
	Primary uint64
	// Secondary is the result under the package's fixed secondary seed.
	Secondary uint64
}

// Sum128 returns a deterministic double hash for key.
func Sum128[K Key](key K) HashPair {
	switch value := any(key).(type) {
	case uint64:
		return hashUint64(value)
	case string:
		return hashString(value)
	case []byte:
		return hashBytes(value)
	case byte:
		return hashUint64(uint64(value))
	case uint:
		return hashUint64(uint64(value))
	case int:
		return hashUint64(uint64(value))
	case int32:
		return hashUint64(uint64(int64(value)))
	case uint32:
		return hashUint64(uint64(value))
	case int64:
		return hashUint64(uint64(value))
	}

	panic("hash: unsupported key type")
}

// Sum64 returns the primary deterministic 64-bit hash for key.
func Sum64[K Key](key K) uint64 {
	return Sum128(key).Primary
}

// Sum64WithSeed returns the deterministic 64-bit hash for key under seed.
// It is intended for deriving independent rows in probabilistic data
// structures; seed is not a security key.
func Sum64WithSeed[K Key](key K, seed uint64) uint64 {
	switch value := any(key).(type) {
	case string:
		return seededString(value, seed)
	case []byte:
		return seededBytes(value, seed)
	case uint64:
		return seededUint64(value, seed)
	case byte:
		return seededUint64(uint64(value), seed)
	case uint:
		return seededUint64(uint64(value), seed)
	case int:
		return seededUint64(uint64(value), seed)
	case int32:
		return seededUint64(uint64(int64(value)), seed)
	case uint32:
		return seededUint64(uint64(value), seed)
	case int64:
		return seededUint64(uint64(value), seed)
	}

	panic("hash: unsupported key type")
}

func hashUint64(value uint64) HashPair {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return hashBytes(encoded[:])
}

func hashString(value string) HashPair {
	return HashPair{
		Primary:   xxhash.Sum64String(value),
		Secondary: seededString(value, secondarySeed),
	}
}

func hashBytes(value []byte) HashPair {
	return HashPair{
		Primary:   xxhash.Sum64(value),
		Secondary: seededBytes(value, secondarySeed),
	}
}

func seededString(value string, seed uint64) uint64 {
	hasher := xxhash.NewWithSeed(seed)
	_, _ = hasher.WriteString(value)
	return hasher.Sum64()
}

func seededBytes(value []byte, seed uint64) uint64 {
	hasher := xxhash.NewWithSeed(seed)
	_, _ = hasher.Write(value)
	return hasher.Sum64()
}

func seededUint64(value uint64, seed uint64) uint64 {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return seededBytes(encoded[:], seed)
}
