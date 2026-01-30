package buffer

import (
	"encoding/binary"
)

// NewSlice creates a Buffer wrapper around an existing byte slice.
func NewSlice(slice []byte) *Buffer {
	return &Buffer{
		offset: uint64(len(slice)),
		data:   slice,
		cap:    cap(slice),
	}
}

// writeLen writes the size header for a slice.
func (b *Buffer) writeLen(n int) {
	buf := b.Allocate(headerSize)
	binary.BigEndian.PutUint64(buf, uint64(n))
}

// SliceAllocate writes the size header and then allocates the space.
// Returns the slice of size n.
func (b *Buffer) SliceAllocate(n int) []byte {
	b.Grow(headerSize + n)
	b.writeLen(n)
	return b.Allocate(n)
}

// WriteSlice writes a byte slice into the buffer as a length-prefixed block.
func (b *Buffer) WriteSlice(p []byte) {
	dst := b.SliceAllocate(len(p))
	copy(dst, p)
}

// Slice returns the byte slice stored at the given offset.
// It also returns the offset of the next slice, or -1 if end reached.
func (b *Buffer) Slice(offset int) ([]byte, int) {
	if offset >= int(b.offset) {
		return nil, -1
	}

	blockLen := binary.BigEndian.Uint64(b.data[offset:])
	payloadStart := offset + headerSize
	nextOffset := payloadStart + int(blockLen)

	payload := b.data[payloadStart:nextOffset]

	if nextOffset >= int(b.offset) {
		nextOffset = -1
	}
	return payload, nextOffset
}
