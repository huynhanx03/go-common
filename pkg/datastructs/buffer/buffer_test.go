package buffer

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// Interface compliance checks (compile-time)
var _ io.Writer = (*Buffer)(nil)
var _ io.WriterTo = (*Buffer)(nil)
var _ io.ReaderFrom = (*Buffer)(nil)

// =============================================================================
// Method: New()
// =============================================================================

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		wantMin  int
	}{
		{"valid_capacity", 1024, 1024},
		{"zero_uses_default", 0, defaultCapacity},
		{"small_uses_default", 10, defaultCapacity},
		{"negative_uses_default", -1, defaultCapacity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(tt.capacity)
			if b == nil {
				t.Fatal("New returned nil")
			}
			if b.cap < tt.wantMin {
				t.Errorf("cap = %d, want >= %d", b.cap, tt.wantMin)
			}
		})
	}
}

func TestNew_InitialState(t *testing.T) {
	b := New(100)
	if !b.IsEmpty() {
		t.Error("new buffer should be empty")
	}
	if b.Len() != headerSize {
		t.Errorf("Len = %d, want %d", b.Len(), headerSize)
	}
}

// =============================================================================
// Method: WithMaxLimit()
// =============================================================================

func TestWithMaxLimit(t *testing.T) {
	tests := []struct {
		name string
		max  int
	}{
		{"set_limit", 1000},
		{"zero_no_limit", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(100).WithMaxLimit(tt.max)
			if b == nil {
				t.Fatal("WithMaxLimit returned nil")
			}
			if b.max != tt.max {
				t.Errorf("max = %d, want %d", b.max, tt.max)
			}
		})
	}
}

func TestWithMaxLimit_Chain(t *testing.T) {
	b := New(100)
	result := b.WithMaxLimit(200)
	if result != b {
		t.Error("WithMaxLimit should return self for chaining")
	}
}

// =============================================================================
// Method: StartOffset()
// =============================================================================

func TestStartOffset(t *testing.T) {
	b := New(100)
	if b.StartOffset() != headerSize {
		t.Errorf("StartOffset = %d, want %d", b.StartOffset(), headerSize)
	}

	// After write
	b.Write([]byte("hello"))
	if b.StartOffset() != headerSize {
		t.Error("StartOffset should not change after write")
	}

	// After reset
	b.Reset()
	if b.StartOffset() != headerSize {
		t.Error("StartOffset should not change after reset")
	}
}

// =============================================================================
// Method: IsEmpty()
// =============================================================================

func TestIsEmpty(t *testing.T) {
	b := New(100)

	// New buffer
	if !b.IsEmpty() {
		t.Error("new buffer should be empty")
	}

	// After write
	b.Write([]byte("data"))
	if b.IsEmpty() {
		t.Error("buffer with data should not be empty")
	}

	// After reset
	b.Reset()
	if !b.IsEmpty() {
		t.Error("buffer after reset should be empty")
	}

	// After allocate
	b.Allocate(10)
	if b.IsEmpty() {
		t.Error("buffer after allocate should not be empty")
	}
}

// =============================================================================
// Method: Len() and LenNoPadding()
// =============================================================================

func TestLen(t *testing.T) {
	b := New(100)

	// New buffer
	if b.Len() != headerSize {
		t.Errorf("new buffer Len = %d, want %d", b.Len(), headerSize)
	}

	// After write
	b.Write(make([]byte, 10))
	if b.Len() != headerSize+10 {
		t.Errorf("after write Len = %d, want %d", b.Len(), headerSize+10)
	}

	// After reset
	b.Reset()
	if b.Len() != headerSize {
		t.Errorf("after reset Len = %d, want %d", b.Len(), headerSize)
	}
}

func TestLenNoPadding(t *testing.T) {
	b := New(100)

	// New buffer
	if b.LenNoPadding() != 0 {
		t.Errorf("new buffer LenNoPadding = %d, want 0", b.LenNoPadding())
	}

	// After write
	b.Write(make([]byte, 10))
	if b.LenNoPadding() != 10 {
		t.Errorf("after write LenNoPadding = %d, want 10", b.LenNoPadding())
	}

	b.Write(make([]byte, 90))
	if b.LenNoPadding() != 100 {
		t.Errorf("after more writes LenNoPadding = %d, want 100", b.LenNoPadding())
	}

	// After reset
	b.Reset()
	if b.LenNoPadding() != 0 {
		t.Errorf("after reset LenNoPadding = %d, want 0", b.LenNoPadding())
	}
}

// =============================================================================
// Method: Bytes()
// =============================================================================

func TestBytes(t *testing.T) {
	b := New(100)

	// Empty
	if len(b.Bytes()) != 0 {
		t.Error("empty buffer Bytes should be empty")
	}

	// After write
	b.Write([]byte("hello"))
	if !bytes.Equal(b.Bytes(), []byte("hello")) {
		t.Errorf("Bytes = %q, want %q", b.Bytes(), "hello")
	}

	// After reset
	b.Reset()
	if len(b.Bytes()) != 0 {
		t.Error("after reset Bytes should be empty")
	}

	// Multiple writes
	b.Write([]byte("A"))
	b.Write([]byte("B"))
	if !bytes.Equal(b.Bytes(), []byte("AB")) {
		t.Errorf("Bytes = %q, want %q", b.Bytes(), "AB")
	}
}

func TestBytes_LargeData(t *testing.T) {
	b := New(100)
	data := make([]byte, 10*1024) // 10KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	b.Write(data)
	if !bytes.Equal(b.Bytes(), data) {
		t.Error("large data mismatch")
	}
}

// =============================================================================
// Method: Grow()
// =============================================================================

func TestGrow(t *testing.T) {
	// Within capacity - no realloc
	b := New(500)
	oldData := b.data
	b.Grow(100)
	if &b.data[0] != &oldData[0] {
		t.Error("Grow within capacity should not reallocate")
	}

	// Exceeds capacity
	b = New(100)
	b.Grow(200)
	if b.cap < 200+headerSize {
		t.Error("Grow should increase capacity")
	}

	// Data preserved
	b = New(100)
	b.Write([]byte("hello"))
	b.Grow(500)
	if !bytes.Equal(b.Bytes(), []byte("hello")) {
		t.Error("Grow should preserve data")
	}

	// Zero grow
	b = New(100)
	oldCap := b.cap
	b.Grow(0)
	if b.cap != oldCap {
		t.Error("Grow(0) should not change capacity")
	}
}

func TestGrow_PanicNilData(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil data")
		}
	}()
	b := New(100)
	b.Release()
	b.Grow(10)
}

func TestGrow_PanicMaxLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on max limit exceeded")
		}
	}()
	b := New(100).WithMaxLimit(200)
	b.Write(make([]byte, 50))
	b.Grow(200) // current + 200 > max
}

// =============================================================================
// Method: Allocate()
// =============================================================================

func TestAllocate(t *testing.T) {
	b := New(200)

	// Happy path
	slice := b.Allocate(20)
	if len(slice) != 20 {
		t.Errorf("Allocate len = %d, want 20", len(slice))
	}

	// Triggers grow
	b = New(100)
	slice = b.Allocate(200)
	if len(slice) != 200 {
		t.Errorf("Allocate after grow len = %d, want 200", len(slice))
	}

	// Zero
	b = New(100)
	slice = b.Allocate(0)
	if len(slice) != 0 {
		t.Error("Allocate(0) should return empty slice")
	}

	// Multiple
	b = New(200)
	b.Allocate(10)
	b.Allocate(10)
	b.Allocate(10)
	if b.LenNoPadding() != 30 {
		t.Errorf("after 3 allocates LenNoPadding = %d, want 30", b.LenNoPadding())
	}
}

func TestAllocate_Writable(t *testing.T) {
	b := New(100)
	slice := b.Allocate(5)
	copy(slice, []byte("hello"))
	if !bytes.Equal(b.Bytes(), []byte("hello")) {
		t.Error("data written to allocated slice should be in buffer")
	}
}

// =============================================================================
// Method: AllocateOffset()
// =============================================================================

func TestAllocateOffset(t *testing.T) {
	b := New(200)

	// First call
	offset := b.AllocateOffset(20)
	if offset != headerSize {
		t.Errorf("first AllocateOffset = %d, want %d", offset, headerSize)
	}

	// Second call
	offset = b.AllocateOffset(20)
	if offset != headerSize+20 {
		t.Errorf("second AllocateOffset = %d, want %d", offset, headerSize+20)
	}

	// Zero
	b = New(100)
	startOffset := b.Len()
	offset = b.AllocateOffset(0)
	if offset != startOffset {
		t.Errorf("AllocateOffset(0) = %d, want %d", offset, startOffset)
	}
}

// =============================================================================
// Method: Write()
// =============================================================================

func TestWrite(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantN   int
		wantErr bool
	}{
		{"valid_data", []byte("hello"), 5, false},
		{"empty_slice", []byte{}, 0, false},
		{"nil_slice", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(100)
			n, err := b.Write(tt.input)
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWrite_Large(t *testing.T) {
	b := New(100)
	data := make([]byte, 1<<20) // 1MB
	n, err := b.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
}

func TestWrite_Multiple(t *testing.T) {
	b := New(100)
	for i := 0; i < 5; i++ {
		b.Write([]byte("X"))
	}
	if !bytes.Equal(b.Bytes(), []byte("XXXXX")) {
		t.Errorf("Bytes = %q, want %q", b.Bytes(), "XXXXX")
	}
}

func TestWrite_AfterReset(t *testing.T) {
	b := New(100)
	b.Write([]byte("old"))
	b.Reset()
	b.Write([]byte("new"))
	if !bytes.Equal(b.Bytes(), []byte("new")) {
		t.Errorf("Bytes = %q, want %q", b.Bytes(), "new")
	}
}

// =============================================================================
// Method: Reset()
// =============================================================================

func TestReset(t *testing.T) {
	b := New(100)
	b.Write([]byte("data"))
	b.Reset()

	if !b.IsEmpty() {
		t.Error("after Reset buffer should be empty")
	}
	if b.Len() != headerSize {
		t.Errorf("after Reset Len = %d, want %d", b.Len(), headerSize)
	}
}

func TestReset_PreservesCap(t *testing.T) {
	b := New(100)
	b.Write(make([]byte, 500)) // trigger grow
	capBefore := b.cap
	b.Reset()
	if b.cap != capBefore {
		t.Error("Reset should preserve capacity")
	}
}

func TestReset_Reusable(t *testing.T) {
	b := New(100)
	b.Write([]byte("first"))
	b.Reset()
	b.Write([]byte("second"))
	if !bytes.Equal(b.Bytes(), []byte("second")) {
		t.Error("buffer should be reusable after Reset")
	}
}

func TestReset_Multiple(t *testing.T) {
	b := New(100)
	for i := 0; i < 3; i++ {
		b.Write([]byte("data"))
		b.Reset()
		if !b.IsEmpty() {
			t.Errorf("reset %d: buffer should be empty", i)
		}
	}
}

// =============================================================================
// Method: Release()
// =============================================================================

func TestRelease(t *testing.T) {
	b := New(100)
	err := b.Release()
	if err != nil {
		t.Errorf("Release error: %v", err)
	}
	if b.data != nil {
		t.Error("after Release data should be nil")
	}
}

func TestRelease_Double(t *testing.T) {
	b := New(100)
	b.Release()
	// Second release should not panic
	err := b.Release()
	if err != nil {
		t.Errorf("second Release error: %v", err)
	}
}

// =============================================================================
// Method: WriteTo()
// =============================================================================

func TestWriteTo(t *testing.T) {
	b := New(100)
	b.Write([]byte("hello"))

	var dst bytes.Buffer
	n, err := b.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if !bytes.Equal(dst.Bytes(), []byte("hello")) {
		t.Errorf("dst = %q, want %q", dst.Bytes(), "hello")
	}
}

func TestWriteTo_Empty(t *testing.T) {
	b := New(100)
	var dst bytes.Buffer
	n, err := b.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

func TestWriteTo_Large(t *testing.T) {
	b := New(100)
	data := make([]byte, 10*1024)
	b.Write(data)

	var dst bytes.Buffer
	n, err := b.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	if n != int64(len(data)) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
}

type errorWriter struct{}

func (w errorWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write error")
}

func TestWriteTo_Error(t *testing.T) {
	b := New(100)
	b.Write([]byte("data"))
	_, err := b.WriteTo(errorWriter{})
	if err == nil {
		t.Error("expected error from WriteTo")
	}
}

// =============================================================================
// Method: ReadFrom()
// =============================================================================

func TestReadFrom(t *testing.T) {
	b := New(100)
	r := strings.NewReader("hello")
	n, err := b.ReadFrom(r)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if !bytes.Equal(b.Bytes(), []byte("hello")) {
		t.Errorf("Bytes = %q, want %q", b.Bytes(), "hello")
	}
}

func TestReadFrom_Large(t *testing.T) {
	b := New(100)
	data := make([]byte, 100*1024) // 100KB
	r := bytes.NewReader(data)
	n, err := b.ReadFrom(r)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != int64(len(data)) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
}

func TestReadFrom_Empty(t *testing.T) {
	b := New(100)
	r := strings.NewReader("")
	n, err := b.ReadFrom(r)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

type errorReader struct{}

func (r errorReader) Read(p []byte) (int, error) {
	return 0, errors.New("read error")
}

func TestReadFrom_Error(t *testing.T) {
	b := New(100)
	_, err := b.ReadFrom(errorReader{})
	if err == nil {
		t.Error("expected error from ReadFrom")
	}
}

// =============================================================================
// Method: Data()
// =============================================================================

func TestData(t *testing.T) {
	b := New(100)

	// Offset 0
	data := b.Data(0)
	if len(data) != b.cap {
		t.Errorf("Data(0) len = %d, want %d", len(data), b.cap)
	}

	// Partial offset
	data = b.Data(50)
	if len(data) != b.cap-50 {
		t.Errorf("Data(50) len = %d, want %d", len(data), b.cap-50)
	}

	// At cap
	data = b.Data(b.cap)
	if len(data) != 0 {
		t.Errorf("Data(cap) len = %d, want 0", len(data))
	}
}

func TestData_PanicOutOfBounds(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on out of bounds")
		}
	}()
	b := New(100)
	b.Data(b.cap + 1)
}

func TestData_AfterGrow(t *testing.T) {
	b := New(100)
	b.Grow(500)
	data := b.Data(0)
	if len(data) != b.cap {
		t.Errorf("Data after grow len = %d, want %d", len(data), b.cap)
	}
}
