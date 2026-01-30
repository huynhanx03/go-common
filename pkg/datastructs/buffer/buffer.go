package buffer

import (
	"fmt"
	"io"
	"sync/atomic"
)

// Buffer is a variable-sized buffer of bytes (append-only) with read capabilities via slice offsets.
// It is NOT thread-safe.
type Buffer struct {
	padding uint64 // reserved starting bytes (for header/metadata)
	offset  uint64 // current write position
	data    []byte // backing storage
	cap     int    // current capacity
	max     int    // maximum allowed capacity (panic if exceeded)
	// ReleaseFn is a callback to return the buffer to a pool.
	// If nil, Release() simply clears the data.
	ReleaseFn func()
}

// New creates and initializes a new Buffer.
func New(capacity int) *Buffer {
	if capacity < defaultCapacity {
		capacity = defaultCapacity
	}
	return &Buffer{
		data:    make([]byte, capacity),
		cap:     capacity,
		offset:  headerSize,
		padding: headerSize,
	}
}

// WithMaxLimit sets the hard limit for buffer growth.
func (b *Buffer) WithMaxLimit(max int) *Buffer {
	b.max = max
	return b
}

// StartOffset returns the offset where data begins (after padding).
func (b *Buffer) StartOffset() int {
	return int(b.padding)
}

// IsEmpty reports whether the buffer is empty.
func (b *Buffer) IsEmpty() bool {
	return int(b.offset) == b.StartOffset()
}

// Len returns the number of bytes written to the buffer (including padding).
func (b *Buffer) Len() int {
	return int(atomic.LoadUint64(&b.offset))
}

// LenNoPadding returns the number of bytes written excluding the initial padding.
func (b *Buffer) LenNoPadding() int {
	return int(atomic.LoadUint64(&b.offset) - b.padding)
}

// Bytes returns the slice holding the written data (excluding padding).
func (b *Buffer) Bytes() []byte {
	off := atomic.LoadUint64(&b.offset)
	return b.data[b.padding:off]
}

// Grow ensures there is space for another n bytes.
func (b *Buffer) Grow(n int) {
	if b.data == nil {
		panic("buffer: uninitialized")
	}
	currentOff := int(b.offset)
	if b.max > 0 && currentOff+n > b.max {
		panic(fmt.Errorf("buffer: max limit exceeded (limit: %d, current: %d, grow: %d)", b.max, b.offset, n))
	}
	if currentOff+n <= b.cap {
		return
	}

	growBy := b.cap + n
	if growBy > maxGrowth { // Cap at 1GB growth steps
		growBy = maxGrowth
	}
	if n > growBy {
		growBy = n
	}
	b.cap += growBy

	newData := make([]byte, b.cap)
	copy(newData, b.data[:b.offset])
	b.data = newData
}

// Allocate returns a slice of size n from the buffer for direct writing.
// The returned slice is valid until the next Grow call.
func (b *Buffer) Allocate(n int) []byte {
	b.Grow(n)
	off := b.offset
	b.offset += uint64(n)
	return b.data[off:int(b.offset)]
}

// AllocateOffset executes Allocate but returns the offset index instead of the slice.
func (b *Buffer) AllocateOffset(n int) int {
	b.Grow(n)
	b.offset += uint64(n)
	return int(b.offset) - n
}

// Write appends p to the buffer (raw write without length header).
func (b *Buffer) Write(p []byte) (n int, err error) {
	n = len(p)
	b.Grow(n)
	copy(b.data[b.offset:], p)
	b.offset += uint64(n)
	return n, nil
}

// Reset resets the buffer offset, effectively clearing it for reuse.
// The underlying memory is retained.
func (b *Buffer) Reset() {
	b.offset = uint64(b.StartOffset())
}

// Release releases the memory used by the buffer or returns it to the pool.
func (b *Buffer) Release() error {
	if b.ReleaseFn != nil {
		b.ReleaseFn()
	} else {
		b.data = nil
	}
	return nil
}

// WriteTo implements io.WriterTo for zero-copy writes to w.
func (b *Buffer) WriteTo(w io.Writer) (int64, error) {
	data := b.Bytes()
	if len(data) == 0 {
		return 0, nil
	}
	n, err := w.Write(data)
	return int64(n), err
}

// ReadFrom implements io.ReaderFrom for efficient reads from r.
func (b *Buffer) ReadFrom(r io.Reader) (int64, error) {
	var total int64
	for {
		// Ensure at least 512 bytes available
		if b.cap-int(b.offset) < 512 {
			b.Grow(512)
		}
		// Read directly into buffer
		n, err := r.Read(b.data[b.offset:b.cap])
		if n > 0 {
			b.offset += uint64(n)
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// Data returns the raw buffer data from offset to current capacity.
func (b *Buffer) Data(offset int) []byte {
	if offset > b.cap {
		panic("buffer: offset out of bounds")
	}
	return b.data[offset:b.cap]
}
