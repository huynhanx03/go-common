package buffer

import (
	"io"
	"sync"
)

// ringBufferPool provides reusable RingBuffer instances.
var ringBufferPool = sync.Pool{
	New: func() any {
		return NewRing(0)
	},
}

// ElasticRing is a lazy-pooled wrapper around RingBuffer.
// It allocates from the pool on first write and returns to pool when empty.
// This provides efficient memory reuse for short-lived buffers.
type ElasticRing struct {
	ring *RingBuffer
}

// getOrCreate returns the underlying RingBuffer, creating one from pool if needed.
func (er *ElasticRing) getOrCreate() *RingBuffer {
	if er.ring == nil {
		er.ring = ringBufferPool.Get().(*RingBuffer)
	}
	return er.ring
}

// returnIfEmpty returns the buffer to pool if it's empty.
func (er *ElasticRing) returnIfEmpty() {
	if er.ring != nil && er.ring.IsEmpty() {
		ringBufferPool.Put(er.ring)
		er.ring = nil
	}
}

// Done returns the underlying buffer to the pool.
// Should be called when the ElasticRing is no longer needed.
func (er *ElasticRing) Done() {
	if er.ring == nil {
		return
	}
	er.ring.Reset()
	ringBufferPool.Put(er.ring)
	er.ring = nil
}

// Peek returns the next n bytes without advancing the read pointer.
// Returns two slices to handle wrap-around case.
func (er *ElasticRing) Peek(n int) (head, tail []byte) {
	if er.ring == nil {
		return nil, nil
	}
	return er.ring.Peek(n)
}

// Discard skips n bytes from the buffer.
// Returns the number of bytes actually discarded.
func (er *ElasticRing) Discard(n int) (int, error) {
	if er.ring == nil {
		return 0, ErrRingEmpty
	}
	defer er.returnIfEmpty()
	return er.ring.Discard(n)
}

// Read implements io.Reader.
// Returns ErrRingEmpty if the buffer is empty.
func (er *ElasticRing) Read(p []byte) (int, error) {
	if er.ring == nil {
		return 0, ErrRingEmpty
	}
	defer er.returnIfEmpty()
	return er.ring.Read(p)
}

// ReadByte reads and returns the next byte from the buffer.
func (er *ElasticRing) ReadByte() (byte, error) {
	if er.ring == nil {
		return 0, ErrRingEmpty
	}
	defer er.returnIfEmpty()
	return er.ring.ReadByte()
}

// Write implements io.Writer.
// Allocates a buffer from pool on first write.
func (er *ElasticRing) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return er.getOrCreate().Write(p)
}

// WriteByte writes a single byte to the buffer.
func (er *ElasticRing) WriteByte(c byte) error {
	return er.getOrCreate().WriteByte(c)
}

// WriteString writes a string to the buffer.
func (er *ElasticRing) WriteString(s string) (int, error) {
	if len(s) == 0 {
		return 0, nil
	}
	return er.getOrCreate().WriteString(s)
}

// Buffered returns the number of bytes available to read.
func (er *ElasticRing) Buffered() int {
	if er.ring == nil {
		return 0
	}
	return er.ring.Buffered()
}

// Available returns the number of bytes available for writing.
func (er *ElasticRing) Available() int {
	if er.ring == nil {
		return 0
	}
	return er.ring.Available()
}

// Len returns the length of underlying buffer slice.
func (er *ElasticRing) Len() int {
	if er.ring == nil {
		return 0
	}
	return er.ring.Len()
}

// Cap returns the capacity of the underlying buffer.
func (er *ElasticRing) Cap() int {
	if er.ring == nil {
		return 0
	}
	return er.ring.Cap()
}

// Bytes returns a copy of all buffered data.
func (er *ElasticRing) Bytes() []byte {
	if er.ring == nil {
		return nil
	}
	return er.ring.Bytes()
}

// ReadFrom implements io.ReaderFrom.
// Reads data from r until EOF and writes it to the buffer.
func (er *ElasticRing) ReadFrom(r io.Reader) (int64, error) {
	return er.getOrCreate().ReadFrom(r)
}

// WriteTo implements io.WriterTo.
// Writes all buffered data to w.
func (er *ElasticRing) WriteTo(w io.Writer) (int64, error) {
	if er.ring == nil {
		return 0, nil
	}
	defer er.returnIfEmpty()
	return er.ring.WriteTo(w)
}

// IsFull returns true if the buffer is full.
func (er *ElasticRing) IsFull() bool {
	if er.ring == nil {
		return false
	}
	return er.ring.IsFull()
}

// IsEmpty returns true if the buffer is empty or not allocated.
func (er *ElasticRing) IsEmpty() bool {
	if er.ring == nil {
		return true
	}
	return er.ring.IsEmpty()
}

// Reset clears the buffer without returning it to the pool.
func (er *ElasticRing) Reset() {
	if er.ring == nil {
		return
	}
	er.ring.Reset()
}
