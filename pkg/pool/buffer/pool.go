package buffer

import (
	"github.com/huynhanx03/go-common/pkg/datastructs/buffer"
	"github.com/huynhanx03/go-common/pkg/pool/internal/calibrated"
)

var defaultPool = calibrated.New(
	// newFunc: create Buffer of given size
	func(size int) *buffer.Buffer {
		return buffer.New(size)
	},
	// sizeFunc: get length of buffer
	func(b *buffer.Buffer) int {
		return b.Len()
	},
	// resetFunc: reset buffer
	func(b *buffer.Buffer) {
		b.Reset()
	},
)

// Get returns a buffer from the default pool.
func Get() *buffer.Buffer {
	return defaultPool.Get(int(defaultPool.DefaultSize()))
}

// GetSize returns a buffer of at least the given size.
func GetSize(size int) *buffer.Buffer {
	return defaultPool.Get(size)
}

// Put returns a buffer to the default pool.
func Put(b *buffer.Buffer) {
	defaultPool.Put(b)
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
