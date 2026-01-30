package buffer

import (
	"errors"
	"io"

	"github.com/huynhanx03/go-common/pkg/pool/byteslice"
	"github.com/huynhanx03/go-common/pkg/utils"
)

const (
	minReadSize       = 512
	defaultRingCap    = 1024     // 1KB
	ringGrowThreshold = 4 * 1024 // 4KB
)

// ErrRingEmpty is returned when trying to read from an empty ring buffer.
var ErrRingEmpty = errors.New("ring buffer is empty")

// RingBuffer is a circular buffer implementing io.ReadWriter.
// It supports auto-grow when write exceeds capacity.
type RingBuffer struct {
	buf      []byte
	capacity int
	readPos  int // next position to read from
	writePos int // next position to write to
	empty    bool
}

// NewRing creates a new RingBuffer with the given initial capacity.
// The capacity will be rounded up to the nearest power of two.
func NewRing(capacity int) *RingBuffer {
	if capacity == 0 {
		return &RingBuffer{empty: true}
	}
	capacity = utils.CeilToPowerOfTwo(capacity)
	return &RingBuffer{
		buf:      byteslice.Get(capacity),
		capacity: capacity,
		empty:    true,
	}
}

// Peek returns the next n bytes without advancing the read pointer.
// Returns two slices to handle wrap-around case.
func (rb *RingBuffer) Peek(n int) (head, tail []byte) {
	if rb.empty {
		return nil, nil
	}
	if n <= 0 {
		return rb.peekAll()
	}

	available := rb.Buffered()
	if n > available {
		n = available
	}

	// Simple case: no wrap-around
	if rb.writePos > rb.readPos {
		head = rb.buf[rb.readPos : rb.readPos+n]
		return head, nil
	}

	// Wrap-around case
	headLen := rb.capacity - rb.readPos
	if n <= headLen {
		head = rb.buf[rb.readPos : rb.readPos+n]
		return head, nil
	}

	head = rb.buf[rb.readPos:]
	tailLen := n - headLen
	tail = rb.buf[:tailLen]
	return head, tail
}

// peekAll returns all buffered data without advancing the read pointer.
func (rb *RingBuffer) peekAll() (head, tail []byte) {
	if rb.empty {
		return nil, nil
	}

	// Simple case: no wrap-around
	if rb.writePos > rb.readPos {
		return rb.buf[rb.readPos:rb.writePos], nil
	}

	// Wrap-around case
	head = rb.buf[rb.readPos:]
	if rb.writePos > 0 {
		tail = rb.buf[:rb.writePos]
	}
	return head, tail
}

// Discard skips n bytes by advancing the read pointer.
// Returns the number of bytes actually discarded.
func (rb *RingBuffer) Discard(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}

	buffered := rb.Buffered()
	if n < buffered {
		rb.readPos = rb.wrapIndex(rb.readPos + n)
		return n, nil
	}

	rb.Reset()
	return buffered, nil
}

// Read implements io.Reader.
// Reads up to len(p) bytes into p and advances the read pointer.
func (rb *RingBuffer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if rb.empty {
		return 0, ErrRingEmpty
	}

	toRead := rb.Buffered()
	if toRead > len(p) {
		toRead = len(p)
	}

	// Simple case: no wrap-around
	if rb.writePos > rb.readPos {
		copy(p, rb.buf[rb.readPos:rb.readPos+toRead])
		rb.readPos += toRead
		if rb.readPos == rb.writePos {
			rb.Reset()
		}
		return toRead, nil
	}

	// Wrap-around case
	headLen := rb.capacity - rb.readPos
	if toRead <= headLen {
		copy(p, rb.buf[rb.readPos:rb.readPos+toRead])
	} else {
		copy(p, rb.buf[rb.readPos:])
		tailLen := toRead - headLen
		copy(p[headLen:], rb.buf[:tailLen])
	}

	rb.readPos = rb.wrapIndex(rb.readPos + toRead)
	if rb.readPos == rb.writePos {
		rb.Reset()
	}
	return toRead, nil
}

// ReadByte reads and returns the next byte from the buffer.
func (rb *RingBuffer) ReadByte() (byte, error) {
	if rb.empty {
		return 0, ErrRingEmpty
	}

	b := rb.buf[rb.readPos]
	rb.readPos++
	if rb.readPos == rb.capacity {
		rb.readPos = 0
	}
	if rb.readPos == rb.writePos {
		rb.Reset()
	}
	return b, nil
}

// Write implements io.Writer.
// Writes p to the buffer, growing if necessary.
func (rb *RingBuffer) Write(p []byte) (int, error) {
	dataLen := len(p)
	if dataLen == 0 {
		return 0, nil
	}

	// Grow buffer if needed
	freeSpace := rb.Available()
	if dataLen > freeSpace {
		rb.grow(rb.capacity + dataLen - freeSpace)
	}

	// Write data, handling wrap-around
	if rb.writePos >= rb.readPos {
		headSpace := rb.capacity - rb.writePos
		if headSpace >= dataLen {
			copy(rb.buf[rb.writePos:], p)
			rb.writePos += dataLen
		} else {
			copy(rb.buf[rb.writePos:], p[:headSpace])
			tailLen := dataLen - headSpace
			copy(rb.buf, p[headSpace:])
			rb.writePos = tailLen
		}
	} else {
		copy(rb.buf[rb.writePos:], p)
		rb.writePos += dataLen
	}

	if rb.writePos == rb.capacity {
		rb.writePos = 0
	}
	rb.empty = false
	return dataLen, nil
}

// WriteByte writes a single byte to the buffer.
func (rb *RingBuffer) WriteByte(c byte) error {
	if rb.Available() < 1 {
		rb.grow(1)
	}

	rb.buf[rb.writePos] = c
	rb.writePos++
	if rb.writePos == rb.capacity {
		rb.writePos = 0
	}
	rb.empty = false
	return nil
}

// WriteString writes a string to the buffer.
func (rb *RingBuffer) WriteString(s string) (int, error) {
	return rb.Write(utils.StringToBytes(s))
}

// Buffered returns the number of bytes available to read.
func (rb *RingBuffer) Buffered() int {
	if rb.readPos == rb.writePos {
		if rb.empty {
			return 0
		}
		return rb.capacity
	}
	if rb.writePos > rb.readPos {
		return rb.writePos - rb.readPos
	}
	return rb.capacity - rb.readPos + rb.writePos
}

// Available returns the number of bytes available for writing.
func (rb *RingBuffer) Available() int {
	if rb.readPos == rb.writePos {
		if rb.empty {
			return rb.capacity
		}
		return 0
	}
	if rb.writePos < rb.readPos {
		return rb.readPos - rb.writePos
	}
	return rb.capacity - rb.writePos + rb.readPos
}

// Len returns the length of underlying buffer slice.
func (rb *RingBuffer) Len() int {
	return len(rb.buf)
}

// Cap returns the capacity of the ring buffer.
func (rb *RingBuffer) Cap() int {
	return rb.capacity
}

// Bytes returns a copy of all buffered data.
func (rb *RingBuffer) Bytes() []byte {
	if rb.empty {
		return nil
	}

	result := make([]byte, 0, rb.Buffered())

	// Simple case: no wrap-around
	if rb.writePos > rb.readPos {
		return append(result, rb.buf[rb.readPos:rb.writePos]...)
	}

	// Wrap-around case
	result = append(result, rb.buf[rb.readPos:]...)
	if rb.writePos > 0 {
		result = append(result, rb.buf[:rb.writePos]...)
	}
	return result
}

// ReadFrom implements io.ReaderFrom.
// Reads data from r until EOF and writes it to the buffer.
func (rb *RingBuffer) ReadFrom(r io.Reader) (int64, error) {
	var total int64

	for {
		// Ensure minimum read space
		if rb.Available() < minReadSize {
			rb.grow(rb.Buffered() + minReadSize)
		}

		bytesRead, err := rb.readFromOnce(r)
		total += bytesRead
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// readFromOnce reads once from the reader into available buffer space.
func (rb *RingBuffer) readFromOnce(r io.Reader) (int64, error) {
	var total int64

	if rb.writePos >= rb.readPos {
		// Read into tail space
		n, err := r.Read(rb.buf[rb.writePos:])
		if n < 0 {
			panic("ring: reader returned negative count")
		}
		rb.empty = false
		rb.writePos = rb.wrapIndex(rb.writePos + n)
		total += int64(n)
		if err != nil {
			return total, err
		}

		// Read into head space (before readPos)
		n, err = r.Read(rb.buf[:rb.readPos])
		if n < 0 {
			panic("ring: reader returned negative count")
		}
		rb.writePos = rb.wrapIndex(rb.writePos + n)
		total += int64(n)
		return total, err
	}

	// writePos < readPos: read into gap
	n, err := r.Read(rb.buf[rb.writePos:rb.readPos])
	if n < 0 {
		panic("ring: reader returned negative count")
	}
	rb.empty = false
	rb.writePos = rb.wrapIndex(rb.writePos + n)
	total += int64(n)
	return total, err
}

// WriteTo implements io.WriterTo.
// Writes all buffered data to w.
func (rb *RingBuffer) WriteTo(w io.Writer) (int64, error) {
	if rb.empty {
		return 0, nil
	}

	// Simple case: no wrap-around
	if rb.writePos > rb.readPos {
		written, err := w.Write(rb.buf[rb.readPos:rb.writePos])
		rb.readPos += written
		if rb.readPos == rb.writePos {
			rb.Reset()
		}
		return int64(written), err
	}

	// Wrap-around case: write tail first, then head
	var total int64

	headLen := rb.capacity - rb.readPos
	written, err := w.Write(rb.buf[rb.readPos:])
	rb.readPos = rb.wrapIndex(rb.readPos + written)
	total += int64(written)
	if err != nil || written < headLen {
		return total, err
	}

	tailLen := rb.writePos
	written, err = w.Write(rb.buf[:tailLen])
	rb.readPos = written
	total += int64(written)
	if rb.readPos == rb.writePos {
		rb.Reset()
	}
	return total, err
}

// IsFull returns true if the buffer is full.
func (rb *RingBuffer) IsFull() bool {
	return rb.readPos == rb.writePos && !rb.empty
}

// IsEmpty returns true if the buffer is empty.
func (rb *RingBuffer) IsEmpty() bool {
	return rb.empty
}

// Reset clears the buffer and resets all pointers.
func (rb *RingBuffer) Reset() {
	rb.empty = true
	rb.readPos = 0
	rb.writePos = 0
}

// wrapIndex returns the index wrapped within buffer capacity.
func (rb *RingBuffer) wrapIndex(idx int) int {
	return idx & (rb.capacity - 1)
}

// grow expands the buffer to at least the specified capacity.
func (rb *RingBuffer) grow(minCap int) {
	newCap := rb.calculateGrowth(minCap)

	newBuf := byteslice.Get(newCap)
	bufferedLen := rb.Buffered()
	_, _ = rb.Read(newBuf)
	byteslice.Put(rb.buf)

	rb.buf = newBuf
	rb.readPos = 0
	rb.writePos = bufferedLen
	rb.capacity = newCap
	if rb.writePos > 0 {
		rb.empty = false
	}
}

// calculateGrowth determines the new capacity based on growth strategy.
func (rb *RingBuffer) calculateGrowth(minCap int) int {
	oldCap := rb.capacity

	// Initial allocation
	if oldCap == 0 {
		if minCap <= defaultRingCap {
			return defaultRingCap
		}
		return utils.CeilToPowerOfTwo(minCap)
	}

	// Growth strategy: double for small buffers, 1.25x for large buffers
	doubleCap := oldCap * 2
	if minCap <= doubleCap {
		if oldCap < ringGrowThreshold {
			return doubleCap
		}
		// Large buffer: grow by 25% until sufficient
		newCap := oldCap
		for newCap > 0 && newCap < minCap {
			newCap += newCap / 4
		}
		if newCap > 0 {
			return newCap
		}
	}
	return minCap
}
