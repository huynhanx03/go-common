package buffer

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// =============================================================================
// Interface Compliance (compile-time)
// =============================================================================

var _ io.Reader = (*RingBuffer)(nil)
var _ io.Writer = (*RingBuffer)(nil)
var _ io.ByteReader = (*RingBuffer)(nil)
var _ io.ByteWriter = (*RingBuffer)(nil)
var _ io.ReaderFrom = (*RingBuffer)(nil)
var _ io.WriterTo = (*RingBuffer)(nil)

// =============================================================================
// Method: NewRing()
// =============================================================================

func TestRing_NewRing(t *testing.T) {
	tests := []struct {
		name     string
		cap      int
		wantCap  int
		wantDiff int // min diff from cap if exact match not expected
	}{
		{"valid_1024", 1024, 1024, 0},
		{"round_up_100", 100, 128, 0},
		{"zero", 0, 0, 0}, // returns empty struct with 0 capacity
		{"min_1", 1, 2, 0},
		{"large_4096", 4096, 4096, 0},
		{"non_power_2_large", 4097, 8192, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rb := NewRing(tt.cap)
			if rb.Cap() != tt.wantCap {
				t.Errorf("NewRing(%d) Cap = %d; want %d", tt.cap, rb.Cap(), tt.wantCap)
			}
			if tt.wantCap > 0 && rb.Bytes() != nil {
				t.Error("NewRing expected empty buffer")
			}
			if rb.Buffered() != 0 {
				t.Errorf("NewRing buffered = %d; want 0", rb.Buffered())
			}
			if rb.Available() != tt.wantCap {
				t.Errorf("NewRing available = %d; want %d", rb.Available(), tt.wantCap)
			}
		})
	}
}

// =============================================================================
// Method: Write()
// =============================================================================

func TestRing_Write(t *testing.T) {
	t.Run("nil_input", func(t *testing.T) {
		rb := NewRing(1024)
		n, err := rb.Write(nil)
		if n != 0 || err != nil {
			t.Errorf("Write(nil) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("empty_slice", func(t *testing.T) {
		rb := NewRing(1024)
		n, err := rb.Write([]byte{})
		if n != 0 || err != nil {
			t.Errorf("Write(empty) = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("happy_path", func(t *testing.T) {
		rb := NewRing(1024)
		data := []byte("hello")
		n, err := rb.Write(data)
		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != len(data) {
			t.Errorf("Write() n = %d; want %d", n, len(data))
		}
		if rb.Buffered() != len(data) {
			t.Errorf("Buffered() = %d; want %d", rb.Buffered(), len(data))
		}
	})

	t.Run("wrap_around", func(t *testing.T) {
		rb := NewRing(16) // Cap 16
		// Advance writePos to 14: write 14 bytes
		_, _ = rb.Write(make([]byte, 14))
		// Advance readPos to 10: read 10 bytes
		_, _ = rb.Read(make([]byte, 10))
		// Now: writePos=14, readPos=10, buffered=4, available=12
		// Write 6 bytes: should wrap (2 at end, 4 at start)
		data := []byte("123456")
		n, err := rb.Write(data)
		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != 6 {
			t.Errorf("Write() n = %d; want 6", n)
		}
		if rb.Buffered() != 10 { // 4 existing + 6 new
			t.Errorf("Buffered() = %d; want 10", rb.Buffered())
		}
		// Verify content
		out := make([]byte, 10)
		_, _ = rb.Read(out)
		// previous 4 bytes were 0s, next 6 are "123456"
		expected := append(make([]byte, 4), []byte("123456")...)
		if !bytes.Equal(out, expected) {
			t.Errorf("Read() got %v; want %v", out, expected)
		}
	})

	t.Run("grow_small", func(t *testing.T) {
		rb := NewRing(16)
		data := make([]byte, 20) // Exceeds 16
		n, err := rb.Write(data)
		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != 20 {
			t.Errorf("Write() n = %d; want 20", n)
		}
		if rb.Cap() < 20 {
			t.Errorf("Cap() = %d; want >= 20", rb.Cap())
		}
		// Expect doubling strategy for small buffers: 16 -> 32
		if rb.Cap() != 32 {
			t.Errorf("Cap() = %d; want 32 per small grow strategy", rb.Cap())
		}
	})

	t.Run("grow_wrap_correctness", func(t *testing.T) {
		// Verify data is correctly realigned after grow when wrapped
		rb := NewRing(16)
		// Write 10
		_, _ = rb.Write(make([]byte, 10))
		// Read 5 -> readPos=5, writePos=10
		_, _ = rb.Read(make([]byte, 5))
		// Write 8 -> wraps: 6 at end (10->16), 2 at start (0->2)
		// Buffer state: [x x 0 0 0 - - - - - 0 0 0 0 0 0] (x=new, 0=old, -=read)
		_, _ = rb.Write(make([]byte, 8))

		// Now force grow by writing more than available
		// Buffered=13, Cap=16, Free=3. Write 10 -> requires grow
		pattern := []byte("0123456789")
		_, _ = rb.Write(pattern)

		// Check integration
		out := make([]byte, rb.Buffered())
		n, _ := rb.Read(out)
		if n != 23 { // 5 (old remaining) + 8 (prev write) + 10 (new)
			t.Errorf("Read() n = %d; want 23", n)
		}
		// Last 10 bytes should match pattern
		if !bytes.Equal(out[13:], pattern) {
			t.Errorf("Suffix mismatch: got %v, want %v", out[13:], pattern)
		}
	})
}

func TestRing_WriteString(t *testing.T) {
	rb := NewRing(1024)
	n, err := rb.WriteString("hello")
	if err != nil {
		t.Errorf("WriteString() error = %v", err)
	}
	if n != 5 {
		t.Errorf("WriteString() n = %d; want 5", n)
	}
	b := make([]byte, 5)
	rb.Read(b)
	if string(b) != "hello" {
		t.Errorf("Read() got %q; want %q", b, "hello")
	}
}

func TestRing_WriteByte(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		rb := NewRing(10)
		err := rb.WriteByte('A')
		if err != nil {
			t.Errorf("WriteByte() error = %v", err)
		}
		if rb.Buffered() != 1 {
			t.Errorf("Buffered() = %d; want 1", rb.Buffered())
		}
		b, _ := rb.ReadByte()
		if b != 'A' {
			t.Errorf("ReadByte() = %c; want 'A'", b)
		}
	})

	t.Run("grow", func(t *testing.T) {
		rb := NewRing(2)
		_ = rb.WriteByte('A')
		_ = rb.WriteByte('B')
		// Full now. Next write should grow.
		err := rb.WriteByte('C')
		if err != nil {
			t.Errorf("WriteByte() error = %v", err)
		}
		if rb.Cap() <= 2 {
			t.Errorf("Cap() did not grow, is %d", rb.Cap())
		}
		// Confirm content
		all := rb.Bytes()
		if string(all) != "ABC" {
			t.Errorf("Bytes() = %q; want %q", all, "ABC")
		}
	})
}

// =============================================================================
// Method: Read()
// =============================================================================

func TestRing_Read(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		rb := NewRing(1024)
		n, err := rb.Read(make([]byte, 10))
		if n != 0 {
			t.Errorf("Read() n = %d; want 0", n)
		}
		if err != ErrRingEmpty {
			t.Errorf("Read() error = %v; want ErrRingEmpty", err)
		}
	})

	t.Run("happy_path", func(t *testing.T) {
		rb := NewRing(1024)
		_, _ = rb.WriteString("hello")
		buf := make([]byte, 10)
		n, err := rb.Read(buf)
		if err != nil {
			t.Errorf("Read() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Read() n = %d; want 5", n)
		}
		if string(buf[:n]) != "hello" {
			t.Errorf("Read() got %q; want %q", buf[:n], "hello")
		}
	})

	t.Run("partial_read", func(t *testing.T) {
		rb := NewRing(1024)
		_, _ = rb.WriteString("hello world")
		buf := make([]byte, 5)
		n, _ := rb.Read(buf)
		if n != 5 {
			t.Errorf("Read() n = %d; want 5", n)
		}
		if string(buf) != "hello" {
			t.Errorf("Read() got %q; want %q", buf, "hello")
		}
		if rb.Buffered() != 6 { // " world" left
			t.Errorf("Buffered() = %d; want 6", rb.Buffered())
		}
	})

	t.Run("wrap_around", func(t *testing.T) {
		rb := NewRing(16)
		// Move to wrap state
		rb.writePos = 14
		rb.readPos = 14
		// Write "ABCDEF" -> 14,15='A','B', 0,1,2,3='C','D','E','F'
		// Note: manually setting Pos is risky, better to use public API
		rb.Reset()
		_, _ = rb.Write(make([]byte, 14))
		_, _ = rb.Read(make([]byte, 14))
		// Now empty, readPos=14, writePos=14

		_, _ = rb.WriteString("ABCDEF")

		buf := make([]byte, 10)
		n, _ := rb.Read(buf)
		if n != 6 {
			t.Errorf("Read() n = %d; want 6", n)
		}
		if string(buf[:n]) != "ABCDEF" {
			t.Errorf("Read() got %q; want %q", buf[:n], "ABCDEF")
		}
	})

	t.Run("nil_dest", func(t *testing.T) {
		rb := NewRing(10)
		_, _ = rb.WriteString("foo")
		n, err := rb.Read(nil)
		if n != 0 || err != nil {
			t.Errorf("Read(nil) = %d, %v; want 0, nil", n, err)
		}
	})
}

func TestRing_ReadByte(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		rb := NewRing(10)
		_ = rb.WriteByte('X')
		b, err := rb.ReadByte()
		if err != nil {
			t.Errorf("ReadByte() error = %v", err)
		}
		if b != 'X' {
			t.Errorf("ReadByte() = %c; want 'X'", b)
		}
		// Now empty
		_, err = rb.ReadByte()
		if err != ErrRingEmpty {
			t.Errorf("ReadByte(empty) error = %v; want ErrRingEmpty", err)
		}
	})
}

// =============================================================================
// Method: Peek()
// =============================================================================

func TestRing_Peek(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		rb := NewRing(1024)
		_, _ = rb.WriteString("hello world")

		head, tail := rb.Peek(5)
		if string(head) != "hello" || tail != nil {
			t.Errorf("Peek(5) = %q, %v; want %q, nil", head, tail, "hello")
		}
		// Confirm read pos didn't move
		if rb.Buffered() != 11 {
			t.Errorf("Buffered() after Peek = %d; want 11", rb.Buffered())
		}
	})

	t.Run("wrap_around", func(t *testing.T) {
		rb := NewRing(16)
		// Write 13 bytes, read 12 Bytes. Leaves 1 byte at index 12. readPos=12.
		// writing 8 bytes will wrap: 3 at end (13-15), 5 at start (0-4).
		_, _ = rb.Write(make([]byte, 13))
		_, _ = rb.Read(make([]byte, 12))

		msg := "ABCDEFGH"
		_, _ = rb.WriteString(msg)

		// Total buffered: 1 (old) + 8 (new) = 9.
		// Head: index 12..15 = 1 (old) + 3 ("ABC").
		// Tail: index 0..4 = 5 ("DEFGH").
		head, tail := rb.Peek(9)

		if len(head) != 4 {
			t.Errorf("Peek head len = %d; want 4", len(head))
		}
		if !bytes.Equal(head[1:], []byte("ABC")) {
			t.Errorf("Peek head[1:] = %q; want ABC", head[1:])
		}

		if string(tail) != "DEFGH" {
			t.Errorf("Peek tail = %q; want DEFGH", tail)
		}
	})

	t.Run("peek_all_zero", func(t *testing.T) {
		rb := NewRing(10)
		_, _ = rb.WriteString("foo")
		head, tail := rb.Peek(0)
		if string(head) != "foo" || tail != nil {
			t.Errorf("Peek(0) = %q, %v; want %q, nil", head, tail, "foo")
		}
	})

	t.Run("peek_too_much", func(t *testing.T) {
		rb := NewRing(10)
		_, _ = rb.WriteString("foo")

		head, _ := rb.Peek(100)
		if string(head) != "foo" {
			t.Errorf("Peek(100) = %q; want %q", head, "foo")
		}
	})

	t.Run("empty", func(t *testing.T) {
		rb := NewRing(10)
		head, tail := rb.Peek(5)
		if head != nil || tail != nil {
			t.Errorf("Peek(empty) = %v, %v; want nil, nil", head, tail)
		}
	})
}

// =============================================================================
// Method: Discard()
// =============================================================================

func TestRing_Discard(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		rb := NewRing(100)
		_, _ = rb.WriteString("hello world")
		n, err := rb.Discard(5)
		if err != nil {
			t.Errorf("Discard() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Discard() n = %d; want 5", n)
		}
		rest, _ := rb.ReadByte()
		if rest != ' ' { // " " is next
			t.Errorf("Next byte after Discard = %c; want ' '", rest)
		}
	})

	t.Run("zero", func(t *testing.T) {
		rb := NewRing(10)
		n, _ := rb.Discard(0)
		if n != 0 {
			t.Errorf("Discard(0) = %d; want 0", n)
		}
	})

	t.Run("all", func(t *testing.T) {
		rb := NewRing(10)
		_, _ = rb.WriteString("foo")
		n, _ := rb.Discard(3)
		if n != 3 {
			t.Errorf("Discard(3) = %d; want 3", n)
		}
		if !rb.IsEmpty() {
			t.Error("Buffer should be empty after Discard(all)")
		}
	})

	t.Run("overflow", func(t *testing.T) {
		rb := NewRing(10)
		_, _ = rb.WriteString("foo")
		n, _ := rb.Discard(100)
		if n != 3 {
			t.Errorf("Discard(100) = %d; want 3 (buffered)", n)
		}
		if !rb.IsEmpty() {
			t.Error("Buffer should be empty after Discard(overflow)")
		}
	})
}

// =============================================================================
// Method: ReadFrom / WriteTo
// =============================================================================

func TestRing_ReadFrom(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		rb := NewRing(100)
		src := strings.NewReader("hello world")
		n, err := rb.ReadFrom(src)
		if err != nil {
			t.Errorf("ReadFrom() error = %v", err)
		}
		if n != 11 {
			t.Errorf("ReadFrom() n = %d; want 11", n)
		}
		if rb.Buffered() != 11 {
			t.Errorf("Buffered() = %d; want 11", rb.Buffered())
		}
	})

	t.Run("grow", func(t *testing.T) {
		rb := NewRing(10) // Small cap
		data := make([]byte, 50)
		src := bytes.NewReader(data)
		n, err := rb.ReadFrom(src)
		if err != nil {
			t.Errorf("ReadFrom() error = %v", err)
		}
		if n != 50 {
			t.Errorf("ReadFrom() n = %d; want 50", n)
		}
		if rb.Cap() < 50 {
			t.Errorf("Buffer did not grow enough, cap = %d", rb.Cap())
		}
	})
}

func TestRing_WriteTo(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		rb := NewRing(100)
		_, _ = rb.WriteString("hello world")

		var dest bytes.Buffer
		n, err := rb.WriteTo(&dest)
		if err != nil {
			t.Errorf("WriteTo() error = %v", err)
		}
		if n != 11 {
			t.Errorf("WriteTo() n = %d; want 11", n)
		}
		if dest.String() != "hello world" {
			t.Errorf("WriteTo output = %q; want %q", dest.String(), "hello world")
		}
		if !rb.IsEmpty() {
			t.Error("Buffer should be empty after WriteTo")
		}
	})

	t.Run("wrap_around", func(t *testing.T) {
		rb := NewRing(16)
		// Setup wrap: 12 bytes buffer, partial wrapping
		rb.writePos = 12
		rb.readPos = 12 // reset logic via public API simulation
		rb.Reset()
		_, _ = rb.Write(make([]byte, 12))
		_, _ = rb.Read(make([]byte, 12))

		_, _ = rb.WriteString("ABCDEFGH") // wrap: ABCD (end), EFGH (start)

		var dest bytes.Buffer
		n, err := rb.WriteTo(&dest)
		if err != nil {
			t.Errorf("WriteTo() error = %v", err)
		}
		if n != 8 {
			t.Errorf("WriteTo() n = %d; want 8", n)
		}
		if dest.String() != "ABCDEFGH" {
			t.Errorf("WriteTo output = %q; want %q", dest.String(), "ABCDEFGH")
		}
	})

	t.Run("empty", func(t *testing.T) {
		rb := NewRing(10)
		var dest bytes.Buffer
		n, err := rb.WriteTo(&dest)
		if n != 0 || err != nil {
			t.Errorf("WriteTo(empty) = %d, %v; want 0, nil", n, err)
		}
	})
}

// =============================================================================
// State Checks
// =============================================================================

func TestRing_StateChecks(t *testing.T) {
	rb := NewRing(8)

	if !rb.IsEmpty() {
		t.Error("New ring should be empty")
	}
	if rb.IsFull() {
		t.Error("New ring should not be full")
	}

	_, _ = rb.WriteString("1234")
	if rb.IsEmpty() || rb.IsFull() {
		t.Error("Partial ring should be neither empty nor full")
	}

	_, _ = rb.WriteString("5678")
	// Note: Write exceeding capacity triggers grow, so it won't be full in sense of "cannot write".
	// But let's check IsFull logic if we filled it exactly to capacity (if it didn't grow).
	// With auto-grow, IsFull is transient.
	// To test IsFull, we need to fill exactly to capacity without triggering grow?
	// Actually Write() with exact fit does trigger grow if Free < dataLen? No.
	// Write checks: if dataLen > freeSpace -> grow.
	// If buffered=8, cap=8, freeSpace=0.

	// Let's create a new ring and fill exactly.
	rb = NewRing(8)
	// Write 8 bytes. freeSpace=8. dataLen=8. 8 > 8 is False. No grow.
	_, _ = rb.Write(make([]byte, 8))

	if !rb.IsFull() {
		t.Error("Filled ring should be IsFull()")
	}
	if rb.IsEmpty() {
		t.Error("Filled ring should not be empty")
	}

	rb.Reset()
	if !rb.IsEmpty() {
		t.Error("Reset ring should be empty")
	}
	if rb.Buffered() != 0 {
		t.Errorf("Buffered post-reset = %d", rb.Buffered())
	}
}

func TestRing_Bytes(t *testing.T) {
	rb := NewRing(16)
	_, _ = rb.WriteString("hello")

	b := rb.Bytes()
	if string(b) != "hello" {
		t.Errorf("Bytes() = %q; want %q", b, "hello")
	}

	// Ensure copy
	b[0] = 'X'
	head, _ := rb.Peek(5)
	if string(head) == "Xello" {
		t.Error("Bytes() returned reference, not copy")
	}
}
