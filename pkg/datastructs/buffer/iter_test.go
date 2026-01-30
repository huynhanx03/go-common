package buffer

import (
	"bytes"
	"errors"
	"testing"
)

// =============================================================================
// Method: SliceIterate()
// =============================================================================

func TestSliceIterate(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() *Buffer
		wantCount  int
		wantData   [][]byte
		wantErr    bool
		errOnSlice int // which slice iteration to return error (-1 = no error)
	}{
		{
			name:       "empty_buffer",
			setup:      func() *Buffer { return New(100) },
			wantCount:  0,
			wantData:   nil,
			wantErr:    false,
			errOnSlice: -1,
		},
		{
			name: "single_slice",
			setup: func() *Buffer {
				b := New(100)
				b.WriteSlice([]byte("hello"))
				return b
			},
			wantCount:  1,
			wantData:   [][]byte{[]byte("hello")},
			wantErr:    false,
			errOnSlice: -1,
		},
		{
			name: "multiple_slices",
			setup: func() *Buffer {
				b := New(200)
				b.WriteSlice([]byte("first"))
				b.WriteSlice([]byte("second"))
				b.WriteSlice([]byte("third"))
				return b
			},
			wantCount:  3,
			wantData:   [][]byte{[]byte("first"), []byte("second"), []byte("third")},
			wantErr:    false,
			errOnSlice: -1,
		},
		{
			name: "after_reset",
			setup: func() *Buffer {
				b := New(100)
				b.WriteSlice([]byte("data"))
				b.Reset()
				return b
			},
			wantCount:  0,
			wantData:   nil,
			wantErr:    false,
			errOnSlice: -1,
		},
		{
			name: "error_on_first_slice",
			setup: func() *Buffer {
				b := New(100)
				b.WriteSlice([]byte("data"))
				return b
			},
			wantCount:  0,
			wantData:   nil,
			wantErr:    true,
			errOnSlice: 0,
		},
		{
			name: "error_on_second_slice",
			setup: func() *Buffer {
				b := New(200)
				b.WriteSlice([]byte("first"))
				b.WriteSlice([]byte("second"))
				b.WriteSlice([]byte("third"))
				return b
			},
			wantCount:  1,
			wantData:   [][]byte{[]byte("first")},
			wantErr:    true,
			errOnSlice: 1,
		},
	}

	errTest := errors.New("test error")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.setup()
			var collected [][]byte
			count := 0

			err := b.SliceIterate(func(p []byte) error {
				if tt.errOnSlice >= 0 && count == tt.errOnSlice {
					return errTest
				}
				// Copy the slice since it's backed by buffer memory
				cp := make([]byte, len(p))
				copy(cp, p)
				collected = append(collected, cp)
				count++
				return nil
			})

			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if count != tt.wantCount {
				t.Errorf("callback count = %d, want %d", count, tt.wantCount)
			}
			if len(collected) != len(tt.wantData) {
				t.Errorf("collected len = %d, want %d", len(collected), len(tt.wantData))
				return
			}
			for i := range tt.wantData {
				if !bytes.Equal(collected[i], tt.wantData[i]) {
					t.Errorf("slice[%d] = %q, want %q", i, collected[i], tt.wantData[i])
				}
			}
		})
	}
}

func TestSliceIterate_LargeData(t *testing.T) {
	b := New(100)
	data := make([]byte, 10*1024) // 10KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	b.WriteSlice(data)

	var collected []byte
	err := b.SliceIterate(func(p []byte) error {
		collected = append(collected, p...)
		return nil
	})

	if err != nil {
		t.Fatalf("SliceIterate error: %v", err)
	}
	if !bytes.Equal(collected, data) {
		t.Error("large data mismatch")
	}
}

func TestSliceIterate_NilCallback(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil callback")
		}
	}()
	b := New(100)
	b.WriteSlice([]byte("data"))
	b.SliceIterate(nil)
}

// =============================================================================
// Method: SliceOffsets()
// =============================================================================

func TestSliceOffsets(t *testing.T) {
	tests := []struct {
		name        string
		setup       func() *Buffer
		wantOffsets []int
	}{
		{
			// Note: SliceOffsets adds StartOffset before checking via Slice,
			// so even an empty buffer returns [headerSize] as the first potential offset
			name:        "empty_buffer",
			setup:       func() *Buffer { return New(100) },
			wantOffsets: []int{headerSize},
		},
		{
			name: "single_slice",
			setup: func() *Buffer {
				b := New(100)
				b.WriteSlice([]byte("test"))
				return b
			},
			wantOffsets: []int{headerSize},
		},
		{
			name: "multiple_slices",
			setup: func() *Buffer {
				b := New(200)
				b.WriteSlice([]byte("abc"))      // offset: headerSize
				b.WriteSlice([]byte("defgh"))    // offset: headerSize + 8 + 3
				b.WriteSlice([]byte("ijklmnop")) // offset: headerSize + 8 + 3 + 8 + 5
				return b
			},
			wantOffsets: []int{
				headerSize,
				headerSize + headerSize + 3,
				headerSize + headerSize + 3 + headerSize + 5,
			},
		},
		{
			// After reset, buffer is empty but SliceOffsets still returns StartOffset
			name: "after_reset",
			setup: func() *Buffer {
				b := New(100)
				b.WriteSlice([]byte("data"))
				b.Reset()
				return b
			},
			wantOffsets: []int{headerSize},
		},
		{
			name: "after_write_new_slice",
			setup: func() *Buffer {
				b := New(100)
				b.WriteSlice([]byte("old"))
				b.Reset()
				b.WriteSlice([]byte("new"))
				return b
			},
			wantOffsets: []int{headerSize},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.setup()
			offsets := b.SliceOffsets()

			if len(offsets) != len(tt.wantOffsets) {
				t.Errorf("offsets len = %d, want %d", len(offsets), len(tt.wantOffsets))
				return
			}
			for i := range tt.wantOffsets {
				if offsets[i] != tt.wantOffsets[i] {
					t.Errorf("offsets[%d] = %d, want %d", i, offsets[i], tt.wantOffsets[i])
				}
			}
		})
	}
}

func TestSliceOffsets_VerifyWithSlice(t *testing.T) {
	// Verify that offsets returned by SliceOffsets can be used with Slice()
	b := New(200)
	testData := [][]byte{
		[]byte("first"),
		[]byte("second"),
		[]byte("third"),
	}
	for _, d := range testData {
		b.WriteSlice(d)
	}

	offsets := b.SliceOffsets()
	if len(offsets) != len(testData) {
		t.Fatalf("offsets len = %d, want %d", len(offsets), len(testData))
	}

	for i, offset := range offsets {
		slice, _ := b.Slice(offset)
		if !bytes.Equal(slice, testData[i]) {
			t.Errorf("Slice(%d) = %q, want %q", offset, slice, testData[i])
		}
	}
}
