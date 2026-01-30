package btree

import "math"

const (
	pageSize    = 4096
	maxKeys     = (pageSize / 16) - 1
	absoluteMax = uint64(math.MaxUint64 - 1)
	minSize     = 1 << 20

	// Layout: [MetaPid | MetaInfo | Keys... | Vals...]
	// Size: 8B (Pid) + 8B (Info) + 8B*N (Keys) + 8B*N (Vals)
	// Metadata is now at the front for better Cache Locality (L1 Cache Hit on search start).
	metaPidIdx  = 0
	metaInfoIdx = 1

	// Offset for Keys and Vals starts after metadata (2 uint64s)
	metaOffset = 2

	// Bitmasks for MetaInfo
	maskNumKeys = uint64(0xFFFFFFFF)         // Lower 32 bits
	bitLeaf     = uint64(1 << 63)            // MSB for Leaf check
	maskBits    = uint64(0xFF00000000000000) // Top 8 bits for flags
)
