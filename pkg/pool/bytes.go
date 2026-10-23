package pool

import "errors"

var (
	// ErrInvalidSize is returned when a size is negative.
	ErrInvalidSize = errors.New("pool: invalid byte-slice size")
)

var bytePool = New(
	func(size int) []byte { return make([]byte, size) },
	func(value []byte) int { return cap(value) },
	func(value []byte) { _ = value[:cap(value)] },
)

// Get returns a pooled byte slice. Negative sizes panic with ErrInvalidSize.
func Get(size int) []byte {
	if size < 0 {
		panic(ErrInvalidSize)
	}
	if size == 0 {
		return []byte{}
	}

	value := bytePool.Get(size)
	return value[:size]
}

// Put returns a byte slice to the pool.
func Put(value []byte) {
	if len(value) == 0 {
		return
	}
	bytePool.Put(value[:cap(value)])
}

// With borrows a slice for fn and returns it afterward. fn must not retain it.
// The slice is not cleared.
func With(size int, fn func([]byte) error) error {
	if size < 0 {
		return ErrInvalidSize
	}
	if size == 0 {
		return fn([]byte{})
	}

	value := Get(size)
	defer Put(value)
	return fn(value)
}

// DefaultSize returns the most used byte size class.
func DefaultSize() uint64 { return bytePool.DefaultSize() }

// MaxSize returns the largest retained byte size class.
func MaxSize() uint64 { return bytePool.MaxSize() }

// GetStats returns byte-pool Put counts since calibration.
func GetStats() PoolStats { return bytePool.GetStats() }
