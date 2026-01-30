package buffer

import (
	"errors"
	"io"
	"math"
)

// ErrNegativeSize is returned when attempting to create a buffer with invalid size.
var ErrNegativeSize = errors.New("negative size is not allowed")

// ElasticBuffer combines ElasticRing and LinkedListBuffer for flexible memory usage.
// The ring buffer is used first (up to maxStaticBytes), then the linked list handles overflow.
// This provides a good balance between memory efficiency and performance.
type ElasticBuffer struct {
	maxStaticBytes int
	ring           ElasticRing
	list           LinkedListBuffer
}

// NewElastic creates a new ElasticBuffer with the given static byte limit.
// The static limit determines when data overflows from ring buffer to linked list.
func NewElastic(maxStaticBytes int) (*ElasticBuffer, error) {
	if maxStaticBytes <= 0 {
		return nil, ErrNegativeSize
	}
	return &ElasticBuffer{maxStaticBytes: maxStaticBytes}, nil
}

// Read implements io.Reader.
// Reads from ring buffer first, then from linked list.
func (eb *ElasticBuffer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	ringRead, err := eb.ring.Read(p)
	if ringRead == len(p) {
		return ringRead, err
	}

	listRead, err := eb.list.Read(p[ringRead:])
	return ringRead + listRead, err
}

// Peek returns up to n bytes as [][]byte without advancing read pointers.
// If n <= 0, returns all buffered data.
func (eb *ElasticBuffer) Peek(n int) ([][]byte, error) {
	if n <= 0 || n == math.MaxInt32 {
		n = math.MaxInt32
	} else if n > eb.Buffered() {
		return nil, io.ErrShortBuffer
	}

	head, tail := eb.ring.Peek(n)

	// Ring buffer has all requested data
	if eb.ring.Buffered() >= n {
		return [][]byte{head, tail}, nil
	}

	// Need to peek from linked list as well
	return eb.list.PeekWithBytes(n, head, tail)
}

// Discard skips n bytes from the buffer.
// Returns the number of bytes actually discarded.
func (eb *ElasticBuffer) Discard(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}

	ringDiscarded, err := eb.ring.Discard(n)
	if ringDiscarded >= n {
		return ringDiscarded, err
	}

	remaining := n - ringDiscarded
	listDiscarded, err := eb.list.Discard(remaining)
	return ringDiscarded + listDiscarded, err
}

// Write implements io.Writer.
// Writes to ring buffer first, overflows to linked list when ring is full.
func (eb *ElasticBuffer) Write(p []byte) (int, error) {
	dataLen := len(p)
	if dataLen == 0 {
		return 0, nil
	}

	// Overflow mode: write directly to list
	if eb.shouldOverflow() {
		eb.list.PushBack(p)
		return dataLen, nil
	}

	// Ring is at capacity: split between ring and list
	if eb.ring.Len() >= eb.maxStaticBytes {
		ringSpace := eb.ring.Available()
		if dataLen > ringSpace {
			_, _ = eb.ring.Write(p[:ringSpace])
			eb.list.PushBack(p[ringSpace:])
			return dataLen, nil
		}
	}

	return eb.ring.Write(p)
}

// Writev writes multiple byte slices to the buffer.
// More efficient than multiple Write calls for scattered data.
func (eb *ElasticBuffer) Writev(slices [][]byte) (int, error) {
	if len(slices) == 0 {
		return 0, nil
	}

	// Overflow mode: write all to list
	if eb.shouldOverflow() {
		return eb.writeAllToList(slices), nil
	}

	return eb.writeSplitRingAndList(slices), nil
}

// writeAllToList writes all slices to the linked list.
func (eb *ElasticBuffer) writeAllToList(slices [][]byte) int {
	var total int
	for _, slice := range slices {
		eb.list.PushBack(slice)
		total += len(slice)
	}
	return total
}

// writeSplitRingAndList writes slices to ring buffer first, overflow to list.
func (eb *ElasticBuffer) writeSplitRingAndList(slices [][]byte) int {
	ringSpace := eb.ring.Available()
	if eb.ring.Len() < eb.maxStaticBytes {
		ringSpace = eb.maxStaticBytes - eb.ring.Buffered()
	}

	var total int
	var sliceIdx int

	// Write to ring while space available
	for sliceIdx < len(slices) {
		slice := slices[sliceIdx]
		sliceLen := len(slice)
		total += sliceLen

		if sliceLen > ringSpace {
			// Split this slice between ring and list
			_, _ = eb.ring.Write(slice[:ringSpace])
			eb.list.PushBack(slice[ringSpace:])
			sliceIdx++
			break
		}

		written, _ := eb.ring.Write(slice)
		ringSpace -= written
		sliceIdx++
	}

	// Write remaining slices to list
	for ; sliceIdx < len(slices); sliceIdx++ {
		slice := slices[sliceIdx]
		total += len(slice)
		eb.list.PushBack(slice)
	}

	return total
}

// shouldOverflow returns true if new writes should go directly to linked list.
func (eb *ElasticBuffer) shouldOverflow() bool {
	return !eb.list.IsEmpty() || eb.ring.Buffered() >= eb.maxStaticBytes
}

// ReadFrom implements io.ReaderFrom.
// Reads from r until EOF, directing data to ring or list based on current state.
func (eb *ElasticBuffer) ReadFrom(r io.Reader) (int64, error) {
	if eb.shouldOverflow() {
		return eb.list.ReadFrom(r)
	}
	return eb.ring.ReadFrom(r)
}

// WriteTo implements io.WriterTo.
// Writes all buffered data to w, draining ring first then list.
func (eb *ElasticBuffer) WriteTo(w io.Writer) (int64, error) {
	ringWritten, err := eb.ring.WriteTo(w)
	if err != nil {
		return ringWritten, err
	}

	listWritten, err := eb.list.WriteTo(w)
	return ringWritten + listWritten, err
}

// Buffered returns the total number of bytes available to read.
func (eb *ElasticBuffer) Buffered() int {
	return eb.ring.Buffered() + eb.list.Buffered()
}

// IsEmpty returns true if both ring and list buffers are empty.
func (eb *ElasticBuffer) IsEmpty() bool {
	return eb.ring.IsEmpty() && eb.list.IsEmpty()
}

// Reset clears both buffers and optionally updates the static byte limit.
// Pass 0 or negative value to keep the current limit.
func (eb *ElasticBuffer) Reset(maxStaticBytes int) {
	eb.ring.Reset()
	eb.list.Reset()
	if maxStaticBytes > 0 {
		eb.maxStaticBytes = maxStaticBytes
	}
}

// Release frees all resources held by the buffer.
// The buffer should not be used after calling Release.
func (eb *ElasticBuffer) Release() {
	eb.ring.Done()
	eb.list.Reset()
}
