package buffer

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// =============================================================================
// Method: NewSlice()
// =============================================================================

func TestNewSlice(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		wantLen   int
		wantCap   int
		wantBytes []byte
	}{
		{"valid_data", []byte("hello"), 5, 5, []byte("hello")},
		{"empty_slice", []byte{}, 0, 0, []byte{}},
		{"nil_slice", nil, 0, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewSlice(tt.input)
			if b == nil {
				t.Fatal("NewSlice returned nil")
			}
			if int(b.offset) != tt.wantLen {
				t.Errorf("offset = %d, want %d", b.offset, tt.wantLen)
			}
			if b.cap != tt.wantCap {
				t.Errorf("cap = %d, want %d", b.cap, tt.wantCap)
			}
		})
	}
}

func TestNewSlice_LargeData(t *testing.T) {
	data := make([]byte, 10*1024) // 10KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	b := NewSlice(data)
	if int(b.offset) != len(data) {
		t.Errorf("offset = %d, want %d", b.offset, len(data))
	}
	if b.cap != cap(data) {
		t.Errorf("cap = %d, want %d", b.cap, cap(data))
	}
}

func TestNewSlice_DataReference(t *testing.T) {
	// Verify buffer references original data (not a copy)
	original := []byte("hello")
	b := NewSlice(original)

	// Modify original
	original[0] = 'X'

	// Buffer should reflect change (same backing array)
	if b.data[0] != 'X' {
		t.Error("NewSlice should reference original slice, not copy")
	}
}

// =============================================================================
// Method: SliceAllocate()
// =============================================================================

func TestSliceAllocate(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		wantLen int
	}{
		{"valid_size", 10, 10},
		{"zero", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(200)
			slice := b.SliceAllocate(tt.n)
			if len(slice) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(slice), tt.wantLen)
			}
		})
	}
}

func TestSliceAllocate_Large(t *testing.T) {
	b := New(100)
	slice := b.SliceAllocate(10 * 1024) // 10KB
	if len(slice) != 10*1024 {
		t.Errorf("len = %d, want %d", len(slice), 10*1024)
	}
}

func TestSliceAllocate_Multiple(t *testing.T) {
	b := New(500)
	startLen := b.Len()

	b.SliceAllocate(10)
	b.SliceAllocate(20)
	b.SliceAllocate(30)

	// Each call adds headerSize (8) + n bytes
	expectedLen := startLen + 3*headerSize + 10 + 20 + 30
	if b.Len() != expectedLen {
		t.Errorf("Len = %d, want %d", b.Len(), expectedLen)
	}
}

func TestSliceAllocate_Writable(t *testing.T) {
	b := New(200)
	slice := b.SliceAllocate(5)
	copy(slice, []byte("hello"))

	// Verify data is in buffer at correct position
	// offset after header should contain "hello"
	startOffset := headerSize + headerSize // buffer padding + slice header
	if !bytes.Equal(b.data[startOffset:startOffset+5], []byte("hello")) {
		t.Error("data written to allocated slice should be in buffer")
	}
}

func TestSliceAllocate_HeaderWritten(t *testing.T) {
	b := New(200)
	n := 42
	b.SliceAllocate(n)

	// Read the header that was written
	headerOffset := headerSize // after buffer padding
	readLen := binary.BigEndian.Uint64(b.data[headerOffset:])
	if readLen != uint64(n) {
		t.Errorf("header = %d, want %d", readLen, n)
	}
}

// =============================================================================
// Method: WriteSlice()
// =============================================================================

func TestWriteSlice(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{"valid_data", []byte("hello")},
		{"empty_slice", []byte{}},
		{"nil_slice", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(200)
			startLen := b.Len()
			b.WriteSlice(tt.input)

			expectedLen := startLen + headerSize + len(tt.input)
			if b.Len() != expectedLen {
				t.Errorf("Len = %d, want %d", b.Len(), expectedLen)
			}
		})
	}
}

func TestWriteSlice_Large(t *testing.T) {
	b := New(100)
	data := make([]byte, 10*1024) // 10KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	b.WriteSlice(data)

	// Read back via Slice
	payload, _ := b.Slice(headerSize)
	if !bytes.Equal(payload, data) {
		t.Error("large data mismatch")
	}
}

func TestWriteSlice_Multiple(t *testing.T) {
	b := New(500)
	b.WriteSlice([]byte("first"))
	b.WriteSlice([]byte("second"))
	b.WriteSlice([]byte("third"))

	// Read all back
	offset := headerSize
	data, next := b.Slice(offset)
	if !bytes.Equal(data, []byte("first")) {
		t.Errorf("first slice = %q, want %q", data, "first")
	}

	data, next = b.Slice(next)
	if !bytes.Equal(data, []byte("second")) {
		t.Errorf("second slice = %q, want %q", data, "second")
	}

	data, _ = b.Slice(next)
	if !bytes.Equal(data, []byte("third")) {
		t.Errorf("third slice = %q, want %q", data, "third")
	}
}

// =============================================================================
// Method: Slice()
// =============================================================================

func TestSlice(t *testing.T) {
	b := New(200)
	testData := []byte("hello")
	b.WriteSlice(testData)

	// Read at headerSize (after buffer padding)
	payload, nextOffset := b.Slice(headerSize)
	if !bytes.Equal(payload, testData) {
		t.Errorf("payload = %q, want %q", payload, testData)
	}
	// Only one slice, so next should be -1
	if nextOffset != -1 {
		t.Errorf("nextOffset = %d, want -1", nextOffset)
	}
}

func TestSlice_BeyondBuffer(t *testing.T) {
	b := New(200)
	b.WriteSlice([]byte("data"))

	// Offset beyond buffer
	payload, next := b.Slice(int(b.offset) + 100)
	if payload != nil {
		t.Errorf("payload = %v, want nil", payload)
	}
	if next != -1 {
		t.Errorf("next = %d, want -1", next)
	}
}

func TestSlice_AtOffset(t *testing.T) {
	b := New(200)
	b.WriteSlice([]byte("data"))

	// Offset exactly at buffer offset (no more data)
	payload, next := b.Slice(int(b.offset))
	if payload != nil {
		t.Errorf("payload = %v, want nil", payload)
	}
	if next != -1 {
		t.Errorf("next = %d, want -1", next)
	}
}

func TestSlice_MultipleReads(t *testing.T) {
	b := New(500)
	expected := []string{"alpha", "beta", "gamma", "delta"}
	for _, s := range expected {
		b.WriteSlice([]byte(s))
	}

	// Iterate through all slices
	offset := headerSize
	var results []string
	for offset != -1 {
		payload, next := b.Slice(offset)
		if payload != nil {
			results = append(results, string(payload))
		}
		offset = next
	}

	if len(results) != len(expected) {
		t.Fatalf("got %d slices, want %d", len(results), len(expected))
	}
	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("slice[%d] = %q, want %q", i, results[i], exp)
		}
	}
}

func TestSlice_AfterReset(t *testing.T) {
	b := New(200)
	b.WriteSlice([]byte("data"))
	b.Reset()

	// After reset, offset is back to headerSize, so Slice should return nil,-1
	payload, next := b.Slice(headerSize)
	if payload != nil {
		t.Errorf("payload after reset = %v, want nil", payload)
	}
	if next != -1 {
		t.Errorf("next after reset = %d, want -1", next)
	}
}

func TestSlice_EmptyPayload(t *testing.T) {
	b := New(200)
	b.WriteSlice([]byte{}) // Empty slice

	payload, next := b.Slice(headerSize)
	if len(payload) != 0 {
		t.Errorf("payload len = %d, want 0", len(payload))
	}
	// Should still be -1 since no more data
	if next != -1 {
		t.Errorf("next = %d, want -1", next)
	}
}

// =============================================================================
// Workflow Tests
// =============================================================================

func TestWorkflow_WriteAndReadMultiple(t *testing.T) {
	b := New(1024)

	// Write multiple slices of varying sizes
	testData := [][]byte{
		[]byte("short"),
		make([]byte, 100),
		[]byte("medium length data here"),
		make([]byte, 500),
	}
	for i, data := range testData {
		if i == 1 || i == 3 {
			// Fill with pattern
			for j := range data {
				data[j] = byte(j % 256)
			}
		}
		b.WriteSlice(data)
	}

	// Read all back
	offset := headerSize
	for i, expected := range testData {
		payload, next := b.Slice(offset)
		if !bytes.Equal(payload, expected) {
			t.Errorf("slice[%d] mismatch", i)
		}
		offset = next
	}
}

func TestWorkflow_NewSliceAndSlice(t *testing.T) {
	// Create a buffer with pre-existing data that looks like a slice
	// This tests NewSlice interaction with Slice method
	data := make([]byte, 100)
	binary.BigEndian.PutUint64(data[0:], 5) // header: length=5
	copy(data[8:13], []byte("hello"))       // payload

	b := NewSlice(data[:13]) // exact size

	// Read via Slice
	payload, next := b.Slice(0)
	if !bytes.Equal(payload, []byte("hello")) {
		t.Errorf("payload = %q, want %q", payload, "hello")
	}
	if next != -1 {
		t.Errorf("next = %d, want -1", next)
	}
}
