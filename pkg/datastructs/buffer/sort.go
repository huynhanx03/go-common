package buffer

import (
	"encoding/binary"
	"sort"
)

// SortSlice sorts the entire buffer using the provided less function.
// It treats the buffer as a collection of length-prefixed slice blocks.
func (b *Buffer) SortSlice(less func(left, right []byte) bool) {
	b.SortSliceBetween(b.StartOffset(), int(b.offset), less)
}

// SortSliceBetween sorts the buffer between start and end offsets.
func (b *Buffer) SortSliceBetween(start, end int, less LessFunc) {
	if start >= end {
		return
	}
	if start == 0 {
		panic("buffer: start offset cannot be zero")
	}

	// Collect offsets of all slices in the range
	var offsets []int
	next, count := start, 0
	for next >= 0 && next < end {
		if count%sortChunkSize == 0 {
			offsets = append(offsets, next)
		}
		_, next = b.Slice(next)
		count++
	}
	if len(offsets) == 0 {
		return
	}

	if offsets[len(offsets)-1] != end {
		offsets = append(offsets, end)
	}

	// Temp buffer for merging
	szTmp := int(float64((end-start)/2) * 1.1)
	s := &sortHelper{
		offsets: offsets,
		b:       b,
		less:    less,
		small:   make([]int, 0, sortChunkSize),
		tmp:     New(szTmp),
	}

	left := offsets[0]
	for _, off := range offsets[1:] {
		s.sortSmall(left, off)
		left = off
	}
	s.sort(0, len(offsets)-1)
}

type LessFunc func(a, b []byte) bool

type sortHelper struct {
	offsets []int
	b       *Buffer
	tmp     *Buffer
	less    LessFunc
	small   []int
}

// sortSmall sorts a small chunk of slices entirely in memory using standard sort.
func (s *sortHelper) sortSmall(start, end int) {
	s.tmp.Reset()
	s.small = s.small[:0]

	next := start
	for next >= 0 && next < end {
		s.small = append(s.small, next)
		_, next = s.b.Slice(next)
	}

	sort.Slice(s.small, func(i, j int) bool {
		left, _ := s.b.Slice(s.small[i])
		right, _ := s.b.Slice(s.small[j])
		return s.less(left, right)
	})

	for _, off := range s.small {
		// rawSlice gets the raw bytes including header
		_, _ = s.tmp.Write(rawSlice(s.b.data[off:]))
	}
	copy(s.b.data[start:end], s.tmp.Bytes())
}

// sort performs merge sort on the chunks.
func (s *sortHelper) sort(lo, hi int) []byte {
	if lo > hi {
		panic("buffer: lo > hi in sort")
	}

	mid := lo + (hi-lo)/2
	loff, hoff := s.offsets[lo], s.offsets[hi]
	if lo == mid {
		return s.b.data[loff:hoff]
	}

	left := s.sort(lo, mid)
	right := s.sort(mid, hi)

	s.merge(left, right, loff, hoff)
	return s.b.data[loff:hoff]
}

func (s *sortHelper) merge(left, right []byte, start, end int) {
	if len(left) == 0 || len(right) == 0 {
		return
	}
	s.tmp.Reset()
	_, _ = s.tmp.Write(left)
	left = s.tmp.Bytes()

	var ls, rs []byte

	copyLeft := func() {
		copy(s.b.data[start:], ls)
		left = left[len(ls):]
		start += len(ls)
	}
	copyRight := func() {
		copy(s.b.data[start:], rs)
		right = right[len(rs):]
		start += len(rs)
	}

	for start < end {
		if len(left) == 0 {
			copy(s.b.data[start:end], right)
			return
		}
		if len(right) == 0 {
			copy(s.b.data[start:end], left)
			return
		}
		ls = rawSlice(left)
		rs = rawSlice(right)

		if s.less(ls[headerSize:], rs[headerSize:]) {
			copyLeft()
		} else {
			copyRight()
		}
	}
}

func rawSlice(p []byte) []byte {
	n := binary.BigEndian.Uint64(p)
	return p[:headerSize+int(n)]
}
