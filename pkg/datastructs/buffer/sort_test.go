package buffer

import (
	"bytes"
	"math/rand"
	"testing"
)

// =============================================================================
// Test Helpers
// =============================================================================

// ascendingLess sorts slices in ascending byte order.
func ascendingLess(a, b []byte) bool {
	return bytes.Compare(a, b) < 0
}

// descendingLess sorts slices in descending byte order.
func descendingLess(a, b []byte) bool {
	return bytes.Compare(a, b) > 0
}

// writeTestSlices writes multiple slices to a buffer.
func writeTestSlices(b *Buffer, data [][]byte) {
	for _, d := range data {
		b.WriteSlice(d)
	}
}

// readAllSlices reads all slices from a buffer into a slice.
func readAllSlices(b *Buffer) [][]byte {
	var result [][]byte
	offset := b.StartOffset()
	for offset != -1 {
		payload, next := b.Slice(offset)
		if payload != nil {
			result = append(result, payload)
		}
		offset = next
	}
	return result
}

// slicesEqual checks if two slice arrays are equal.
func slicesEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// =============================================================================
// Method: SortSlice()
// =============================================================================

func TestSortSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    [][]byte
		less     LessFunc
		expected [][]byte
	}{
		{
			name:     "ascending_order",
			input:    [][]byte{[]byte("c"), []byte("a"), []byte("b")},
			less:     ascendingLess,
			expected: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
		},
		{
			name:     "descending_order",
			input:    [][]byte{[]byte("a"), []byte("b"), []byte("c")},
			less:     descendingLess,
			expected: [][]byte{[]byte("c"), []byte("b"), []byte("a")},
		},
		{
			name:     "already_sorted",
			input:    [][]byte{[]byte("a"), []byte("b"), []byte("c")},
			less:     ascendingLess,
			expected: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
		},
		{
			name:     "reverse_sorted",
			input:    [][]byte{[]byte("c"), []byte("b"), []byte("a")},
			less:     ascendingLess,
			expected: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
		},
		{
			name:     "two_slices_swap",
			input:    [][]byte{[]byte("b"), []byte("a")},
			less:     ascendingLess,
			expected: [][]byte{[]byte("a"), []byte("b")},
		},
		{
			name:     "numeric_data",
			input:    [][]byte{{3}, {1}, {2}},
			less:     ascendingLess,
			expected: [][]byte{{1}, {2}, {3}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(1024)
			writeTestSlices(b, tt.input)
			b.SortSlice(tt.less)
			result := readAllSlices(b)
			if !slicesEqual(result, tt.expected) {
				t.Errorf("SortSlice() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSortSlice_Empty(t *testing.T) {
	b := New(1024)
	// No slices written
	b.SortSlice(ascendingLess) // Should not panic
	result := readAllSlices(b)
	if len(result) != 0 {
		t.Errorf("empty buffer should have 0 slices, got %d", len(result))
	}
}

func TestSortSlice_Single(t *testing.T) {
	b := New(1024)
	b.WriteSlice([]byte("only"))
	b.SortSlice(ascendingLess)
	result := readAllSlices(b)
	if len(result) != 1 || !bytes.Equal(result[0], []byte("only")) {
		t.Errorf("single slice should remain unchanged, got %v", result)
	}
}

func TestSortSlice_LargeData(t *testing.T) {
	b := New(1024)
	count := 2000 // > sortChunkSize (1024)

	// Generate random data
	input := make([][]byte, count)
	for i := 0; i < count; i++ {
		data := make([]byte, 4)
		data[0] = byte(rand.Intn(256))
		data[1] = byte(rand.Intn(256))
		data[2] = byte(rand.Intn(256))
		data[3] = byte(rand.Intn(256))
		input[i] = data
	}
	writeTestSlices(b, input)

	b.SortSlice(ascendingLess)

	result := readAllSlices(b)
	if len(result) != count {
		t.Fatalf("got %d slices, want %d", len(result), count)
	}

	// Verify sorted order
	for i := 1; i < len(result); i++ {
		if bytes.Compare(result[i-1], result[i]) > 0 {
			t.Errorf("not sorted at index %d: %v > %v", i, result[i-1], result[i])
			break
		}
	}
}

// =============================================================================
// Method: SortSliceBetween()
// =============================================================================

func TestSortSliceBetween(t *testing.T) {
	tests := []struct {
		name     string
		input    [][]byte
		startIdx int // which slice offset to use as start (-1 for StartOffset)
		endIdx   int // which slice offset to use as end (-1 for Len)
		expected [][]byte
	}{
		{
			name:     "sort_all",
			input:    [][]byte{[]byte("c"), []byte("a"), []byte("b")},
			startIdx: -1, // StartOffset
			endIdx:   -1, // Len
			expected: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
		},
		{
			name:     "sort_middle_only",
			input:    [][]byte{[]byte("d"), []byte("c"), []byte("b"), []byte("a")},
			startIdx: 1,
			endIdx:   3,
			expected: [][]byte{[]byte("d"), []byte("b"), []byte("c"), []byte("a")},
		},
		{
			name:     "sort_first_two",
			input:    [][]byte{[]byte("b"), []byte("a"), []byte("c"), []byte("d")},
			startIdx: -1,
			endIdx:   2,
			expected: [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")},
		},
		{
			name:     "sort_last_two",
			input:    [][]byte{[]byte("a"), []byte("b"), []byte("d"), []byte("c")},
			startIdx: 2,
			endIdx:   -1,
			expected: [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(1024)
			writeTestSlices(b, tt.input)

			// Get slice offsets
			offsets := b.SliceOffsets()

			var start, end int
			if tt.startIdx == -1 {
				start = b.StartOffset()
			} else {
				start = offsets[tt.startIdx]
			}
			if tt.endIdx == -1 {
				end = b.Len()
			} else {
				end = offsets[tt.endIdx]
			}

			b.SortSliceBetween(start, end, ascendingLess)

			result := readAllSlices(b)
			if !slicesEqual(result, tt.expected) {
				t.Errorf("SortSliceBetween() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSortSliceBetween_StartEqualsEnd(t *testing.T) {
	b := New(1024)
	writeTestSlices(b, [][]byte{[]byte("c"), []byte("a"), []byte("b")})

	// start == end should be no-op
	b.SortSliceBetween(100, 100, ascendingLess)

	result := readAllSlices(b)
	expected := [][]byte{[]byte("c"), []byte("a"), []byte("b")}
	if !slicesEqual(result, expected) {
		t.Errorf("start==end should be no-op, got %v", result)
	}
}

func TestSortSliceBetween_StartGreaterThanEnd(t *testing.T) {
	b := New(1024)
	writeTestSlices(b, [][]byte{[]byte("c"), []byte("a"), []byte("b")})

	// start > end should be no-op
	b.SortSliceBetween(200, 100, ascendingLess)

	result := readAllSlices(b)
	expected := [][]byte{[]byte("c"), []byte("a"), []byte("b")}
	if !slicesEqual(result, expected) {
		t.Errorf("start>end should be no-op, got %v", result)
	}
}

func TestSortSliceBetween_LargeData(t *testing.T) {
	b := New(1024)
	count := 2000 // > sortChunkSize

	input := make([][]byte, count)
	for i := 0; i < count; i++ {
		input[i] = []byte{byte(rand.Intn(256)), byte(rand.Intn(256))}
	}
	writeTestSlices(b, input)

	b.SortSliceBetween(b.StartOffset(), b.Len(), ascendingLess)

	result := readAllSlices(b)
	for i := 1; i < len(result); i++ {
		if bytes.Compare(result[i-1], result[i]) > 0 {
			t.Errorf("not sorted at index %d", i)
			break
		}
	}
}

// =============================================================================
// Panic Tests
// =============================================================================

func TestPanic_SortSliceBetween_StartZero(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic when start == 0")
		}
		if msg, ok := r.(string); ok {
			if msg != "buffer: start offset cannot be zero" {
				t.Errorf("wrong panic message: %s", msg)
			}
		}
	}()

	b := New(1024)
	b.WriteSlice([]byte("test"))
	b.SortSliceBetween(0, b.Len(), ascendingLess) // Should panic
}

func TestPanic_SortSlice_NilComparator(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil comparator")
		}
	}()

	b := New(1024)
	b.WriteSlice([]byte("a"))
	b.WriteSlice([]byte("b"))
	b.SortSlice(nil) // Should panic
}

func TestPanic_SortSliceBetween_ReleasedBuffer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on released buffer")
		}
	}()

	b := New(1024)
	b.WriteSlice([]byte("test"))
	b.Release()
	b.SortSlice(ascendingLess) // Should panic - nil data
}

// =============================================================================
// Workflow Tests
// =============================================================================

func TestWorkflow_MultipleSort(t *testing.T) {
	b := New(1024)
	writeTestSlices(b, [][]byte{[]byte("c"), []byte("a"), []byte("b")})

	// Sort ascending
	b.SortSlice(ascendingLess)
	result := readAllSlices(b)
	expected := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	if !slicesEqual(result, expected) {
		t.Errorf("after ascending sort: %v, want %v", result, expected)
	}

	// Sort descending
	b.SortSlice(descendingLess)
	result = readAllSlices(b)
	expected = [][]byte{[]byte("c"), []byte("b"), []byte("a")}
	if !slicesEqual(result, expected) {
		t.Errorf("after descending sort: %v, want %v", result, expected)
	}
}

func TestWorkflow_SortVariableSizeSlices(t *testing.T) {
	b := New(1024)
	input := [][]byte{
		[]byte("longer_string"),
		[]byte("a"),
		[]byte("medium"),
		[]byte("bb"),
	}
	writeTestSlices(b, input)

	b.SortSlice(ascendingLess)

	result := readAllSlices(b)
	expected := [][]byte{
		[]byte("a"),
		[]byte("bb"),
		[]byte("longer_string"),
		[]byte("medium"),
	}
	if !slicesEqual(result, expected) {
		t.Errorf("variable size sort: %v, want %v", result, expected)
	}
}
