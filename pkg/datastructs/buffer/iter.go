package buffer

// SliceIterate iterates over the buffer and calls the provided function for each slice.
// It stops iteration if the function returns an error.
func (b *Buffer) SliceIterate(fn func(p []byte) error) error {
	if b.IsEmpty() {
		return nil
	}

	next := b.StartOffset()
	var p []byte
	for next >= 0 {
		p, next = b.Slice(next)
		if len(p) == 0 {
			continue
		}
		if err := fn(p); err != nil {
			return err
		}
	}
	return nil
}

// SliceOffsets returns a list of all slice offsets in the buffer.
// Warning: This traverses the entire buffer and allocates a slice.
func (b *Buffer) SliceOffsets() []int {
	next := b.StartOffset()
	var offsets []int
	for next >= 0 {
		offsets = append(offsets, next)
		_, next = b.Slice(next)
	}
	return offsets
}
