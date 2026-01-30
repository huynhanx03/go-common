package buffer

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// =============================================================================
// Interface Compliance (compile-time)
// =============================================================================

var _ io.Reader = (*ElasticBuffer)(nil)
var _ io.Writer = (*ElasticBuffer)(nil)
var _ io.ReaderFrom = (*ElasticBuffer)(nil)
var _ io.WriterTo = (*ElasticBuffer)(nil)

// =============================================================================
// Method: NewElastic()
// =============================================================================

func TestElastic_NewElastic(t *testing.T) {
	tests := []struct {
		name           string
		maxStaticBytes int
		wantErr        error
	}{
		{"valid_1024", 1024, nil},
		{"min_valid_1", 1, nil},
		{"large_1MB", 1 << 20, nil},
		{"zero", 0, ErrNegativeSize},
		{"negative", -1, ErrNegativeSize},
		{"negative_large", -1000, ErrNegativeSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eb, err := NewElastic(tt.maxStaticBytes)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("NewElastic(%d) error = %v, want %v", tt.maxStaticBytes, err, tt.wantErr)
			}
			if tt.wantErr == nil && eb == nil {
				t.Error("NewElastic() returned nil buffer with no error")
			}
			if tt.wantErr != nil && eb != nil {
				t.Error("NewElastic() returned non-nil buffer with error")
			}
		})
	}
}

// =============================================================================
// Method: Read()
// =============================================================================

func TestElastic_Read(t *testing.T) {
	t.Run("nil_input", func(t *testing.T) {
		eb, _ := NewElastic(100)
		n, err := eb.Read(nil)
		if n != 0 || err != nil {
			t.Errorf("Read(nil) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("empty_slice", func(t *testing.T) {
		eb, _ := NewElastic(100)
		n, err := eb.Read([]byte{})
		if n != 0 || err != nil {
			t.Errorf("Read(empty) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("empty_buffer", func(t *testing.T) {
		eb, _ := NewElastic(100)
		buf := make([]byte, 10)
		n, err := eb.Read(buf)
		// ElasticRing returns ErrRingEmpty when empty
		if n != 0 {
			t.Errorf("Read(empty buffer) n = %d; want 0", n)
		}
		if err == nil {
			t.Error("Read(empty buffer) expected error, got nil")
		}
	})

	t.Run("happy_path_ring_only", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello world")
		_, _ = eb.Write(data)

		buf := make([]byte, len(data))
		n, err := eb.Read(buf)
		if err != nil {
			t.Errorf("Read() error = %v", err)
		}
		if n != len(data) {
			t.Errorf("Read() n = %d; want %d", n, len(data))
		}
		if !bytes.Equal(buf, data) {
			t.Errorf("Read() got %q; want %q", buf, data)
		}
	})

	t.Run("partial_read", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello world")
		_, _ = eb.Write(data)

		buf := make([]byte, 5)
		n, _ := eb.Read(buf)
		if n != 5 {
			t.Errorf("Read() n = %d; want 5", n)
		}
		if string(buf) != "hello" {
			t.Errorf("Read() got %q; want %q", buf, "hello")
		}
	})

	t.Run("span_ring_and_list", func(t *testing.T) {
		// Create buffer with small static limit to force overflow
		eb, _ := NewElastic(10)
		ringData := []byte("ring")
		listData := []byte("list_overflow")

		// Write ring data
		_, _ = eb.Write(ringData)
		// Fill ring to capacity, then overflow to list
		_, _ = eb.Write(make([]byte, 10))
		_, _ = eb.Write(listData)

		// Read all data
		buf := make([]byte, 100)
		n, _ := eb.Read(buf)
		if n == 0 {
			t.Error("Read() expected data spanning ring and list")
		}
	})

	t.Run("list_only", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Fill ring completely
		_, _ = eb.Write(make([]byte, 10))
		// Add to list
		listData := []byte("list data")
		_, _ = eb.Write(listData)

		// Drain ring first
		ringBuf := make([]byte, 10)
		_, _ = eb.Read(ringBuf)

		// Now read from list
		buf := make([]byte, 20)
		n, _ := eb.Read(buf)
		if n != len(listData) {
			t.Errorf("Read() from list n = %d; want %d", n, len(listData))
		}
	})
}

// =============================================================================
// Method: Peek()
// =============================================================================

func TestElastic_Peek(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello world")
		_, _ = eb.Write(data)

		slices, err := eb.Peek(5)
		if err != nil {
			t.Errorf("Peek() error = %v", err)
		}

		var total int
		for _, s := range slices {
			total += len(s)
		}
		if total < 5 {
			t.Errorf("Peek(5) got %d bytes; want 5", total)
		}
	})

	t.Run("zero_returns_all", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello")
		_, _ = eb.Write(data)

		slices, err := eb.Peek(0)
		if err != nil {
			t.Errorf("Peek(0) error = %v", err)
		}

		var total int
		for _, s := range slices {
			total += len(s)
		}
		if total != len(data) {
			t.Errorf("Peek(0) got %d bytes; want %d", total, len(data))
		}
	})

	t.Run("negative_returns_all", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello")
		_, _ = eb.Write(data)

		slices, err := eb.Peek(-1)
		if err != nil {
			t.Errorf("Peek(-1) error = %v", err)
		}

		var total int
		for _, s := range slices {
			total += len(s)
		}
		if total != len(data) {
			t.Errorf("Peek(-1) got %d bytes; want %d", total, len(data))
		}
	})

	t.Run("error_short_buffer", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("hello"))

		_, err := eb.Peek(100) // Request more than available
		if !errors.Is(err, io.ErrShortBuffer) {
			t.Errorf("Peek(too much) error = %v; want ErrShortBuffer", err)
		}
	})

	t.Run("empty_buffer_peek_exceeds", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, err := eb.Peek(10)
		if err == nil {
			t.Error("Peek(10) on empty buffer expected error")
		}
	})

	t.Run("span_ring_and_list", func(t *testing.T) {
		eb, _ := NewElastic(5)
		// Force data to both ring and list
		_, _ = eb.Write([]byte("ring"))
		_, _ = eb.Write([]byte("12345")) // Fill ring
		_, _ = eb.Write([]byte("list"))  // Overflow to list

		slices, err := eb.Peek(0) // Get all
		if err != nil {
			t.Errorf("Peek() error = %v", err)
		}
		if slices == nil {
			t.Error("Peek() expected non-nil slices")
		}
	})

	t.Run("does_not_advance_pointer", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello")
		_, _ = eb.Write(data)

		_, _ = eb.Peek(5)
		// Buffered should still be same
		if eb.Buffered() != len(data) {
			t.Errorf("Peek() advanced buffer; Buffered() = %d; want %d", eb.Buffered(), len(data))
		}
	})
}

// =============================================================================
// Method: Discard()
// =============================================================================

func TestElastic_Discard(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello world")
		_, _ = eb.Write(data)

		n, _ := eb.Discard(5)
		if n != 5 {
			t.Errorf("Discard(5) = %d; want 5", n)
		}
		if eb.Buffered() != len(data)-5 {
			t.Errorf("Buffered() after Discard = %d; want %d", eb.Buffered(), len(data)-5)
		}
	})

	t.Run("zero_discards_nothing", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("hello"))

		n, _ := eb.Discard(0)
		if n != 0 {
			t.Errorf("Discard(0) = %d; want 0", n)
		}
	})

	t.Run("negative_discards_nothing", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("hello"))

		n, _ := eb.Discard(-1)
		if n != 0 {
			t.Errorf("Discard(-1) = %d; want 0", n)
		}
	})

	t.Run("discard_exceeds_available", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello")
		_, _ = eb.Write(data)

		n, _ := eb.Discard(100)
		if n != len(data) {
			t.Errorf("Discard(100) = %d; want %d (available)", n, len(data))
		}
	})

	t.Run("empty_buffer", func(t *testing.T) {
		eb, _ := NewElastic(100)
		n, _ := eb.Discard(10)
		if n != 0 {
			t.Errorf("Discard(10) on empty = %d; want 0", n)
		}
	})

	t.Run("span_ring_and_list", func(t *testing.T) {
		eb, _ := NewElastic(5)
		// Force data to both ring and list
		_, _ = eb.Write([]byte("ring1")) // 5 bytes to ring
		_, _ = eb.Write([]byte("list1")) // Goes to list

		totalBefore := eb.Buffered()
		discarded, _ := eb.Discard(7) // Discard across ring+list
		if discarded != 7 {
			t.Errorf("Discard(7) = %d; want 7", discarded)
		}
		if eb.Buffered() != totalBefore-7 {
			t.Errorf("Buffered() after Discard = %d; want %d", eb.Buffered(), totalBefore-7)
		}
	})
}

// =============================================================================
// Method: Write()
// =============================================================================

func TestElastic_Write(t *testing.T) {
	t.Run("nil_input", func(t *testing.T) {
		eb, _ := NewElastic(100)
		n, err := eb.Write(nil)
		if n != 0 || err != nil {
			t.Errorf("Write(nil) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("empty_slice", func(t *testing.T) {
		eb, _ := NewElastic(100)
		n, err := eb.Write([]byte{})
		if n != 0 || err != nil {
			t.Errorf("Write(empty) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("happy_path", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello world")
		n, err := eb.Write(data)
		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != len(data) {
			t.Errorf("Write() = %d; want %d", n, len(data))
		}
		if eb.Buffered() != len(data) {
			t.Errorf("Buffered() = %d; want %d", eb.Buffered(), len(data))
		}
	})

	t.Run("fill_ring_exactly", func(t *testing.T) {
		eb, _ := NewElastic(10)
		data := make([]byte, 10)
		n, _ := eb.Write(data)
		if n != 10 {
			t.Errorf("Write(10 bytes) = %d; want 10", n)
		}
	})

	t.Run("overflow_to_list", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Fill ring
		_, _ = eb.Write(make([]byte, 10))
		// Overflow to list
		overflow := []byte("overflow")
		n, _ := eb.Write(overflow)
		if n != len(overflow) {
			t.Errorf("Write(overflow) = %d; want %d", n, len(overflow))
		}
		if eb.Buffered() != 10+len(overflow) {
			t.Errorf("Buffered() = %d; want %d", eb.Buffered(), 10+len(overflow))
		}
	})

	t.Run("split_ring_and_list", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Partial fill ring
		_, _ = eb.Write(make([]byte, 6))
		// Write that spans ring and list
		span := make([]byte, 10)
		n, _ := eb.Write(span)
		if n != 10 {
			t.Errorf("Write(spanning) = %d; want 10", n)
		}
	})

	t.Run("overflow_mode_all_to_list", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Fill ring
		_, _ = eb.Write(make([]byte, 10))
		// Add to list to enter overflow mode
		_, _ = eb.Write([]byte("list"))
		// Now any write should go to list
		more := []byte("more")
		n, _ := eb.Write(more)
		if n != len(more) {
			t.Errorf("Write(in overflow mode) = %d; want %d", n, len(more))
		}
	})
}

// =============================================================================
// Method: Writev()
// =============================================================================

func TestElastic_Writev(t *testing.T) {
	t.Run("nil_input", func(t *testing.T) {
		eb, _ := NewElastic(100)
		n, err := eb.Writev(nil)
		if n != 0 || err != nil {
			t.Errorf("Writev(nil) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("empty_slices", func(t *testing.T) {
		eb, _ := NewElastic(100)
		n, err := eb.Writev([][]byte{})
		if n != 0 || err != nil {
			t.Errorf("Writev(empty) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("happy_path", func(t *testing.T) {
		eb, _ := NewElastic(100)
		slices := [][]byte{[]byte("hello"), []byte(" "), []byte("world")}
		n, err := eb.Writev(slices)
		if err != nil {
			t.Errorf("Writev() error = %v", err)
		}
		expected := 5 + 1 + 5
		if n != expected {
			t.Errorf("Writev() = %d; want %d", n, expected)
		}
	})

	t.Run("single_slice", func(t *testing.T) {
		eb, _ := NewElastic(100)
		slices := [][]byte{[]byte("hello")}
		n, _ := eb.Writev(slices)
		if n != 5 {
			t.Errorf("Writev(single) = %d; want 5", n)
		}
	})

	t.Run("split_ring_and_list", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Partial fill
		_, _ = eb.Write(make([]byte, 5))
		// Writev that spans
		slices := [][]byte{make([]byte, 6), make([]byte, 4)}
		n, _ := eb.Writev(slices)
		if n != 10 {
			t.Errorf("Writev(spanning) = %d; want 10", n)
		}
	})

	t.Run("overflow_mode", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Enter overflow mode
		_, _ = eb.Write(make([]byte, 10))
		_, _ = eb.Write([]byte("list"))

		slices := [][]byte{[]byte("a"), []byte("b")}
		n, _ := eb.Writev(slices)
		if n != 2 {
			t.Errorf("Writev(overflow mode) = %d; want 2", n)
		}
	})

	t.Run("mixed_sizes", func(t *testing.T) {
		eb, _ := NewElastic(100)
		slices := [][]byte{
			[]byte("a"),
			make([]byte, 50),
			[]byte("bc"),
		}
		n, _ := eb.Writev(slices)
		expected := 1 + 50 + 2
		if n != expected {
			t.Errorf("Writev(mixed) = %d; want %d", n, expected)
		}
	})
}

// =============================================================================
// Method: ReadFrom()
// =============================================================================

func TestElastic_ReadFrom(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("hello world")
		reader := bytes.NewReader(data)

		n, err := eb.ReadFrom(reader)
		if err != nil {
			t.Errorf("ReadFrom() error = %v", err)
		}
		if n != int64(len(data)) {
			t.Errorf("ReadFrom() = %d; want %d", n, len(data))
		}
	})

	t.Run("empty_reader", func(t *testing.T) {
		eb, _ := NewElastic(100)
		reader := bytes.NewReader(nil)

		n, err := eb.ReadFrom(reader)
		if err != nil {
			t.Errorf("ReadFrom(empty) error = %v", err)
		}
		if n != 0 {
			t.Errorf("ReadFrom(empty) = %d; want 0", n)
		}
	})

	t.Run("overflow_mode_to_list", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Enter overflow mode
		_, _ = eb.Write(make([]byte, 10))
		_, _ = eb.Write([]byte("list"))

		reader := bytes.NewReader([]byte("from reader"))
		n, err := eb.ReadFrom(reader)
		if err != nil {
			t.Errorf("ReadFrom(overflow) error = %v", err)
		}
		if n != 11 {
			t.Errorf("ReadFrom(overflow) = %d; want 11", n)
		}
	})

	t.Run("error_reader", func(t *testing.T) {
		eb, _ := NewElastic(100)
		reader := errorReader{}

		_, err := eb.ReadFrom(reader)
		if err == nil {
			t.Error("ReadFrom(error) expected error")
		}
	})

	t.Run("large_data", func(t *testing.T) {
		eb, _ := NewElastic(1024)
		data := make([]byte, 10000)
		for i := range data {
			data[i] = byte(i % 256)
		}
		reader := bytes.NewReader(data)

		n, err := eb.ReadFrom(reader)
		if err != nil {
			t.Errorf("ReadFrom(large) error = %v", err)
		}
		if n != int64(len(data)) {
			t.Errorf("ReadFrom(large) = %d; want %d", n, len(data))
		}
	})
}

// =============================================================================
// Method: WriteTo()
// =============================================================================

func TestElastic_WriteTo(t *testing.T) {
	t.Run("happy_path_ring_and_list", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Write to ring and list
		_, _ = eb.Write([]byte("ring1"))
		_, _ = eb.Write([]byte("12345")) // Fill ring
		_, _ = eb.Write([]byte("list1")) // To list

		var buf bytes.Buffer
		n, err := eb.WriteTo(&buf)
		if err != nil {
			t.Errorf("WriteTo() error = %v", err)
		}
		if n != 15 {
			t.Errorf("WriteTo() = %d; want 15", n)
		}
	})

	t.Run("ring_only", func(t *testing.T) {
		eb, _ := NewElastic(100)
		data := []byte("ring only")
		_, _ = eb.Write(data)

		var buf bytes.Buffer
		n, err := eb.WriteTo(&buf)
		if err != nil {
			t.Errorf("WriteTo() error = %v", err)
		}
		if n != int64(len(data)) {
			t.Errorf("WriteTo() = %d; want %d", n, len(data))
		}
		if buf.String() != string(data) {
			t.Errorf("WriteTo() content = %q; want %q", buf.String(), data)
		}
	})

	t.Run("empty_buffer", func(t *testing.T) {
		eb, _ := NewElastic(100)
		var buf bytes.Buffer
		n, err := eb.WriteTo(&buf)
		// Empty buffer may return ErrRingEmpty from ring
		if n != 0 {
			t.Errorf("WriteTo(empty) = %d; want 0", n)
		}
		_ = err // Error is acceptable for empty buffer
	})

	t.Run("ring_error", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("data"))

		writer := errorWriter{}

		_, err := eb.WriteTo(writer)
		if err == nil {
			t.Error("WriteTo(error) expected error")
		}
	})

	t.Run("list_only", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Fill ring
		_, _ = eb.Write(make([]byte, 10))
		// Add to list
		_, _ = eb.Write([]byte("list"))
		// Drain ring
		ringBuf := make([]byte, 10)
		_, _ = eb.Read(ringBuf)

		// Now only list has data
		var buf bytes.Buffer
		n, _ := eb.WriteTo(&buf)
		if n != 4 {
			t.Errorf("WriteTo(list only) = %d; want 4", n)
		}
	})

	t.Run("drains_buffer", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("data"))

		var buf bytes.Buffer
		_, _ = eb.WriteTo(&buf)

		// Buffer should be empty after WriteTo
		if !eb.IsEmpty() {
			t.Errorf("IsEmpty() after WriteTo = false; want true")
		}
	})
}

// =============================================================================
// Method: Buffered()
// =============================================================================

func TestElastic_Buffered(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		eb, _ := NewElastic(100)
		if eb.Buffered() != 0 {
			t.Errorf("Buffered() = %d; want 0", eb.Buffered())
		}
	})

	t.Run("ring_only", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("hello"))
		if eb.Buffered() != 5 {
			t.Errorf("Buffered() = %d; want 5", eb.Buffered())
		}
	})

	t.Run("list_only", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Fill ring and read it
		_, _ = eb.Write(make([]byte, 10))
		_, _ = eb.Write([]byte("list"))
		ringBuf := make([]byte, 10)
		_, _ = eb.Read(ringBuf)

		if eb.Buffered() != 4 {
			t.Errorf("Buffered() = %d; want 4", eb.Buffered())
		}
	})

	t.Run("both_ring_and_list", func(t *testing.T) {
		eb, _ := NewElastic(10)
		_, _ = eb.Write([]byte("ring12345")) // 9 bytes to ring
		_, _ = eb.Write([]byte("12"))        // 1 to ring, 1 to list
		_, _ = eb.Write([]byte("list"))      // 4 to list

		// Ring: 10, List: 5
		if eb.Buffered() != 15 {
			t.Errorf("Buffered() = %d; want 15", eb.Buffered())
		}
	})
}

// =============================================================================
// Method: IsEmpty()
// =============================================================================

func TestElastic_IsEmpty(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		eb, _ := NewElastic(100)
		if !eb.IsEmpty() {
			t.Error("IsEmpty() = false; want true")
		}
	})

	t.Run("ring_has_data", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("a"))
		if eb.IsEmpty() {
			t.Error("IsEmpty() = true; want false")
		}
	})

	t.Run("list_has_data", func(t *testing.T) {
		eb, _ := NewElastic(10)
		// Fill ring and overflow to list
		_, _ = eb.Write(make([]byte, 10))
		_, _ = eb.Write([]byte("list"))
		// Drain ring
		ringBuf := make([]byte, 10)
		_, _ = eb.Read(ringBuf)

		if eb.IsEmpty() {
			t.Error("IsEmpty() = true; want false (list has data)")
		}
	})

	t.Run("both_have_data", func(t *testing.T) {
		eb, _ := NewElastic(10)
		_, _ = eb.Write(make([]byte, 10))
		_, _ = eb.Write([]byte("list"))
		if eb.IsEmpty() {
			t.Error("IsEmpty() = true; want false")
		}
	})
}

// =============================================================================
// Method: Reset()
// =============================================================================

func TestElastic_Reset(t *testing.T) {
	t.Run("clears_and_updates_limit", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("data"))

		eb.Reset(200)
		if !eb.IsEmpty() {
			t.Error("IsEmpty() after Reset = false; want true")
		}
	})

	t.Run("zero_keeps_limit", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("data"))

		eb.Reset(0)
		if !eb.IsEmpty() {
			t.Error("IsEmpty() after Reset(0) = false; want true")
		}
	})

	t.Run("negative_keeps_limit", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("data"))

		eb.Reset(-1)
		if !eb.IsEmpty() {
			t.Error("IsEmpty() after Reset(-1) = false; want true")
		}
	})

	t.Run("empty_buffer_reset", func(t *testing.T) {
		eb, _ := NewElastic(100)
		eb.Reset(50) // Should not panic
		if !eb.IsEmpty() {
			t.Error("IsEmpty() after Reset(empty) = false; want true")
		}
	})

	t.Run("after_partial_read", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("hello world"))
		buf := make([]byte, 5)
		_, _ = eb.Read(buf)

		eb.Reset(0)
		if !eb.IsEmpty() {
			t.Error("IsEmpty() after Reset = false; want true")
		}
		if eb.Buffered() != 0 {
			t.Errorf("Buffered() after Reset = %d; want 0", eb.Buffered())
		}
	})
}

// =============================================================================
// Method: Release()
// =============================================================================

func TestElastic_Release(t *testing.T) {
	t.Run("has_data", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("data"))
		eb.Release() // Should not panic
	})

	t.Run("empty_buffer", func(t *testing.T) {
		eb, _ := NewElastic(100)
		eb.Release() // Should not panic
	})

	t.Run("double_release", func(t *testing.T) {
		eb, _ := NewElastic(100)
		_, _ = eb.Write([]byte("data"))
		eb.Release()
		eb.Release() // Should not panic
	})
}

// =============================================================================
// Sequence Tests
// =============================================================================

func TestElastic_Workflow_WriteReadReset(t *testing.T) {
	eb, _ := NewElastic(100)

	// Write
	data := []byte("hello world")
	n, _ := eb.Write(data)
	if n != len(data) {
		t.Fatalf("Write() = %d; want %d", n, len(data))
	}

	// Read partial
	buf := make([]byte, 5)
	n, _ = eb.Read(buf)
	if n != 5 {
		t.Fatalf("Read() = %d; want 5", n)
	}
	if string(buf) != "hello" {
		t.Fatalf("Read() got %q; want %q", buf, "hello")
	}

	// Reset
	eb.Reset(0)
	if !eb.IsEmpty() {
		t.Fatalf("IsEmpty() after Reset = false; want true")
	}

	// Write again
	newData := []byte("new data")
	n, _ = eb.Write(newData)
	if n != len(newData) {
		t.Fatalf("Write() after Reset = %d; want %d", n, len(newData))
	}
	if eb.Buffered() != len(newData) {
		t.Fatalf("Buffered() = %d; want %d", eb.Buffered(), len(newData))
	}
}

func TestElastic_Workflow_OverflowMode(t *testing.T) {
	eb, _ := NewElastic(10)

	// Fill ring exactly
	_, _ = eb.Write(make([]byte, 10))

	// Write more to trigger overflow
	overflowData := []byte("overflow data")
	n, _ := eb.Write(overflowData)
	if n != len(overflowData) {
		t.Fatalf("Write(overflow) = %d; want %d", n, len(overflowData))
	}

	// Total should be ring + list
	expected := 10 + len(overflowData)
	if eb.Buffered() != expected {
		t.Fatalf("Buffered() = %d; want %d", eb.Buffered(), expected)
	}

	// Drain all via WriteTo
	var buf bytes.Buffer
	written, _ := eb.WriteTo(&buf)
	if written != int64(expected) {
		t.Fatalf("WriteTo() = %d; want %d", written, expected)
	}

	// Should be empty now
	if !eb.IsEmpty() {
		t.Fatal("IsEmpty() after WriteTo = false; want true")
	}
}

// Test helpers (errorReader and errorWriter) are defined in buffer_test.go
