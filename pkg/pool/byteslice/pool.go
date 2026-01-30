package byteslice

import (
	"github.com/huynhanx03/go-common/pkg/pool/internal/calibrated"
)

var defaultPool = calibrated.New(
	// newFunc: create []byte of given size
	func(size int) []byte {
		return make([]byte, size)
	},
	// sizeFunc: get capacity of slice
	func(b []byte) int {
		return cap(b)
	},
	// resetFunc: reset slice (just expand to full capacity)
	func(b []byte) {
		_ = b[:cap(b)]
	},
)

// Get returns a byte slice of at least the given size from the pool.
func Get(size int) []byte {
	b := defaultPool.Get(size)
	return b[:size]
}

// Put returns a byte slice to the pool.
func Put(b []byte) {
	if len(b) == 0 {
		return
	}
	defaultPool.Put(b[:cap(b)])
}

// DefaultSize returns the calibrated default size.
func DefaultSize() uint64 {
	return defaultPool.DefaultSize()
}

// MaxSize returns the calibrated max size (95th percentile).
func MaxSize() uint64 {
	return defaultPool.MaxSize()
}

// GetStats returns allocation counts per bucket.
func GetStats() [calibrated.Steps]uint64 {
	return defaultPool.GetStats()
}

// BucketSize returns the size of bucket at index i.
func BucketSize(i int) int {
	return calibrated.BucketSize(i)
}
