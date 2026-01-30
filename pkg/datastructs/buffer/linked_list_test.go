package buffer

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"
)

// =============================================================================
// Interface Compliance (compile-time)
// =============================================================================

var _ io.Reader = (*LinkedListBuffer)(nil)
var _ io.ReaderFrom = (*LinkedListBuffer)(nil)
var _ io.WriterTo = (*LinkedListBuffer)(nil)

// =============================================================================
// Test Helpers
// =============================================================================

// llErrorReader returns an error on Read.
type llErrorReader struct{}

func (r llErrorReader) Read(p []byte) (int, error) {
	return 0, errors.New("read error")
}

// negativeReader returns negative count (should cause panic).
type negativeReader struct{}

func (r negativeReader) Read(p []byte) (int, error) {
	return -1, nil
}

// errorWriter returns an error on Write.
type llErrorWriter struct{}

func (w llErrorWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write error")
}

// shortWriter writes less than provided.
type shortWriter struct{}

func (w shortWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		return 1, nil // Always write only 1 byte
	}
	return len(p), nil
}

// =============================================================================
// Method: Read()
// =============================================================================

func TestLinkedListBuffer_Read(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() *LinkedListBuffer
		bufSize    int
		wantN      int
		wantErr    error
		wantData   []byte
		wantRemain int // remaining bytes after read
	}{
		{
			name: "happy_read",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			bufSize:    10,
			wantN:      5,
			wantErr:    nil,
			wantData:   []byte("hello"),
			wantRemain: 0,
		},
		{
			name: "nil_buffer",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("data"))
				return ll
			},
			bufSize:    0, // will use nil
			wantN:      0,
			wantErr:    nil,
			wantData:   nil,
			wantRemain: 4,
		},
		{
			name: "empty_buffer",
			setup: func() *LinkedListBuffer {
				return &LinkedListBuffer{}
			},
			bufSize:    10,
			wantN:      0,
			wantErr:    io.EOF,
			wantData:   []byte{},
			wantRemain: 0,
		},
		{
			name: "partial_read",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("helloworld"))
				return ll
			},
			bufSize:    5,
			wantN:      5,
			wantErr:    nil,
			wantData:   []byte("hello"),
			wantRemain: 5,
		},
		{
			name: "multi_node_read",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("AB"))
				ll.PushBack([]byte("CD"))
				return ll
			},
			bufSize:    4,
			wantN:      4,
			wantErr:    nil,
			wantData:   []byte("ABCD"),
			wantRemain: 0,
		},
		{
			name: "partial_multi_node",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("ABC"))
				ll.PushBack([]byte("DEF"))
				return ll
			},
			bufSize:    4,
			wantN:      4,
			wantErr:    nil,
			wantData:   []byte("ABCD"),
			wantRemain: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := tt.setup()
			var p []byte
			if tt.bufSize > 0 {
				p = make([]byte, tt.bufSize)
			}

			n, err := ll.Read(p)
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantData != nil && !bytes.Equal(p[:n], tt.wantData) {
				t.Errorf("data = %q, want %q", p[:n], tt.wantData)
			}
			if ll.Buffered() != tt.wantRemain {
				t.Errorf("remaining = %d, want %d", ll.Buffered(), tt.wantRemain)
			}
		})
	}
}

// =============================================================================
// Method: AllocNode() and FreeNode()
// =============================================================================

func TestLinkedListBuffer_AllocNode(t *testing.T) {
	ll := &LinkedListBuffer{}

	tests := []struct {
		name string
		size int
	}{
		{"happy", 100},
		{"zero", 0},
		{"large", 1 << 20}, // 1MB
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := ll.AllocNode(tt.size)
			if len(buf) != tt.size {
				t.Errorf("len = %d, want %d", len(buf), tt.size)
			}
			ll.FreeNode(buf)
		})
	}
}

func TestLinkedListBuffer_FreeNode_Nil(t *testing.T) {
	ll := &LinkedListBuffer{}
	// Should not panic
	ll.FreeNode(nil)
}

// =============================================================================
// Method: Append()
// =============================================================================

func TestLinkedListBuffer_Append(t *testing.T) {
	tests := []struct {
		name       string
		inputs     [][]byte
		wantLen    int
		wantBufed  int
		wantIsEmpt bool
	}{
		{
			name:       "happy",
			inputs:     [][]byte{[]byte("hello")},
			wantLen:    1,
			wantBufed:  5,
			wantIsEmpt: false,
		},
		{
			name:       "nil_no_op",
			inputs:     [][]byte{nil},
			wantLen:    0,
			wantBufed:  0,
			wantIsEmpt: true,
		},
		{
			name:       "empty_no_op",
			inputs:     [][]byte{{}},
			wantLen:    0,
			wantBufed:  0,
			wantIsEmpt: true,
		},
		{
			name:       "multi_append",
			inputs:     [][]byte{[]byte("A"), []byte("BC"), []byte("DEF")},
			wantLen:    3,
			wantBufed:  6,
			wantIsEmpt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := &LinkedListBuffer{}
			for _, in := range tt.inputs {
				// For Append, we need pool-allocated slices
				if len(in) > 0 {
					buf := ll.AllocNode(len(in))
					copy(buf, in)
					ll.Append(buf)
				} else {
					ll.Append(in)
				}
			}

			if ll.Len() != tt.wantLen {
				t.Errorf("Len = %d, want %d", ll.Len(), tt.wantLen)
			}
			if ll.Buffered() != tt.wantBufed {
				t.Errorf("Buffered = %d, want %d", ll.Buffered(), tt.wantBufed)
			}
			if ll.IsEmpty() != tt.wantIsEmpt {
				t.Errorf("IsEmpty = %v, want %v", ll.IsEmpty(), tt.wantIsEmpt)
			}
		})
	}
}

// =============================================================================
// Method: Pop()
// =============================================================================

func TestLinkedListBuffer_Pop(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("first"))
		ll.PushBack([]byte("second"))

		data := ll.Pop()
		if !bytes.Equal(data, []byte("first")) {
			t.Errorf("Pop = %q, want %q", data, "first")
		}
		ll.FreeNode(data)

		if ll.Len() != 1 {
			t.Errorf("Len = %d, want 1", ll.Len())
		}
	})

	t.Run("empty", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		data := ll.Pop()
		if data != nil {
			t.Errorf("Pop from empty = %v, want nil", data)
		}
	})

	t.Run("single_node", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("only"))

		data := ll.Pop()
		ll.FreeNode(data)

		if !ll.IsEmpty() {
			t.Error("after pop single node, should be empty")
		}
	})

	t.Run("multi_pop", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("A"))
		ll.PushBack([]byte("B"))
		ll.PushBack([]byte("C"))

		for i, want := range []string{"A", "B", "C"} {
			data := ll.Pop()
			if !bytes.Equal(data, []byte(want)) {
				t.Errorf("Pop %d = %q, want %q", i, data, want)
			}
			ll.FreeNode(data)
		}

		if !ll.IsEmpty() {
			t.Error("after all pops, should be empty")
		}
	})
}

// =============================================================================
// Method: PushFront()
// =============================================================================

func TestLinkedListBuffer_PushFront(t *testing.T) {
	tests := []struct {
		name      string
		initial   [][]byte
		pushData  []byte
		wantFirst []byte
		wantLen   int
	}{
		{
			name:      "happy",
			initial:   [][]byte{[]byte("second")},
			pushData:  []byte("first"),
			wantFirst: []byte("first"),
			wantLen:   2,
		},
		{
			name:      "empty_data",
			initial:   [][]byte{[]byte("only")},
			pushData:  nil,
			wantFirst: []byte("only"),
			wantLen:   1,
		},
		{
			name:      "empty_buffer",
			initial:   nil,
			pushData:  []byte("first"),
			wantFirst: []byte("first"),
			wantLen:   1,
		},
		{
			name:      "multi",
			initial:   [][]byte{[]byte("B"), []byte("C")},
			pushData:  []byte("A"),
			wantFirst: []byte("A"),
			wantLen:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := &LinkedListBuffer{}
			for _, d := range tt.initial {
				ll.PushBack(d)
			}

			ll.PushFront(tt.pushData)

			if ll.Len() != tt.wantLen {
				t.Errorf("Len = %d, want %d", ll.Len(), tt.wantLen)
			}

			first := ll.Pop()
			if !bytes.Equal(first, tt.wantFirst) {
				t.Errorf("first = %q, want %q", first, tt.wantFirst)
			}
			ll.FreeNode(first)
		})
	}
}

// =============================================================================
// Method: PushBack()
// =============================================================================

func TestLinkedListBuffer_PushBack(t *testing.T) {
	tests := []struct {
		name     string
		inputs   [][]byte
		wantLen  int
		wantData string // concatenated
	}{
		{
			name:     "happy",
			inputs:   [][]byte{[]byte("hello")},
			wantLen:  1,
			wantData: "hello",
		},
		{
			name:     "empty_data",
			inputs:   [][]byte{nil},
			wantLen:  0,
			wantData: "",
		},
		{
			name:     "multi",
			inputs:   [][]byte{[]byte("A"), []byte("B"), []byte("C")},
			wantLen:  3,
			wantData: "ABC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := &LinkedListBuffer{}
			for _, in := range tt.inputs {
				ll.PushBack(in)
			}

			if ll.Len() != tt.wantLen {
				t.Errorf("Len = %d, want %d", ll.Len(), tt.wantLen)
			}

			// Read all data
			buf := make([]byte, 100)
			n, _ := ll.Read(buf)
			if string(buf[:n]) != tt.wantData {
				t.Errorf("data = %q, want %q", buf[:n], tt.wantData)
			}
		})
	}
}

// =============================================================================
// Method: Peek()
// =============================================================================

func TestLinkedListBuffer_Peek(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *LinkedListBuffer
		maxBytes int
		wantLen  int
		wantErr  error
	}{
		{
			name: "happy",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			maxBytes: 3,
			wantLen:  3,
			wantErr:  nil,
		},
		{
			name: "zero_returns_all",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			maxBytes: 0,
			wantLen:  5,
			wantErr:  nil,
		},
		{
			name: "negative_returns_all",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			maxBytes: -1,
			wantLen:  5,
			wantErr:  nil,
		},
		{
			name: "maxint_returns_all",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			maxBytes: math.MaxInt32,
			wantLen:  5,
			wantErr:  nil,
		},
		{
			name: "exceeds_buffer",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			maxBytes: 100,
			wantErr:  io.ErrShortBuffer,
		},
		{
			name: "empty_buffer",
			setup: func() *LinkedListBuffer {
				return &LinkedListBuffer{}
			},
			maxBytes: 10,
			wantErr:  io.ErrShortBuffer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := tt.setup()
			slices, err := ll.Peek(tt.maxBytes)

			if err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil {
				total := 0
				for _, s := range slices {
					total += len(s)
				}
				if total != tt.wantLen {
					t.Errorf("total bytes = %d, want %d", total, tt.wantLen)
				}
			}
		})
	}

	t.Run("non_consuming", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("hello"))

		before := ll.Buffered()
		ll.Peek(3)
		after := ll.Buffered()

		if before != after {
			t.Errorf("Peek should not consume: before=%d, after=%d", before, after)
		}
	})
}

// =============================================================================
// Method: PeekWithBytes()
// =============================================================================

func TestLinkedListBuffer_PeekWithBytes(t *testing.T) {
	t.Run("with_existing", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("world"))

		existing := [][]byte{[]byte("hello")}
		slices, err := ll.PeekWithBytes(10, existing...)
		if err != nil {
			t.Fatalf("err = %v", err)
		}

		// Should have existing first
		if len(slices) < 2 {
			t.Fatal("expected at least 2 slices")
		}
		if !bytes.Equal(slices[0], []byte("hello")) {
			t.Errorf("first = %q, want %q", slices[0], "hello")
		}
	})

	t.Run("no_existing", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("data"))

		slices, err := ll.PeekWithBytes(4)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(slices) != 1 {
			t.Errorf("slices = %d, want 1", len(slices))
		}
	})

	t.Run("exceeds", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("short"))

		_, err := ll.PeekWithBytes(100)
		if err != io.ErrShortBuffer {
			t.Errorf("err = %v, want ErrShortBuffer", err)
		}
	})

	t.Run("empty_existing_skipped", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("data"))

		existing := [][]byte{nil, {}}
		slices, _ := ll.PeekWithBytes(0, existing...)

		// Empty slices should be skipped
		for _, s := range slices {
			if len(s) == 0 {
				t.Error("empty slice should be skipped")
			}
		}
	})
}

// =============================================================================
// Method: Discard()
// =============================================================================

func TestLinkedListBuffer_Discard(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() *LinkedListBuffer
		n          int
		wantN      int
		wantRemain int
	}{
		{
			name: "happy",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			n:          3,
			wantN:      3,
			wantRemain: 2,
		},
		{
			name: "zero",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			n:          0,
			wantN:      0,
			wantRemain: 5,
		},
		{
			name: "negative",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			n:          -1,
			wantN:      0,
			wantRemain: 5,
		},
		{
			name: "exceeds",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))
				return ll
			},
			n:          100,
			wantN:      5,
			wantRemain: 0,
		},
		{
			name: "empty_buffer",
			setup: func() *LinkedListBuffer {
				return &LinkedListBuffer{}
			},
			n:          10,
			wantN:      0,
			wantRemain: 0,
		},
		{
			name: "partial_node",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("ABCDE"))
				return ll
			},
			n:          3,
			wantN:      3,
			wantRemain: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := tt.setup()
			n, err := ll.Discard(tt.n)

			if err != nil {
				t.Errorf("err = %v", err)
			}
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if ll.Buffered() != tt.wantRemain {
				t.Errorf("remain = %d, want %d", ll.Buffered(), tt.wantRemain)
			}
		})
	}

	t.Run("partial_verifies_remaining", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("ABCDE"))

		ll.Discard(3) // Discard "ABC"

		// Remaining should be "DE"
		buf := make([]byte, 10)
		n, _ := ll.Read(buf)
		if string(buf[:n]) != "DE" {
			t.Errorf("remaining = %q, want %q", buf[:n], "DE")
		}
	})
}

// =============================================================================
// Method: ReadFrom()
// =============================================================================

func TestLinkedListBuffer_ReadFrom(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		r := bytes.NewReader([]byte("hello world"))

		n, err := ll.ReadFrom(r)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if n != 11 {
			t.Errorf("n = %d, want 11", n)
		}
		if ll.Buffered() != 11 {
			t.Errorf("Buffered = %d, want 11", ll.Buffered())
		}
	})

	t.Run("empty_reader", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		r := bytes.NewReader([]byte{})

		n, err := ll.ReadFrom(r)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if n != 0 {
			t.Errorf("n = %d, want 0", n)
		}
	})

	t.Run("error_reader", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		_, err := ll.ReadFrom(llErrorReader{})
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("large", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		data := make([]byte, 1<<20) // 1MB
		r := bytes.NewReader(data)

		n, err := ll.ReadFrom(r)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if n != int64(len(data)) {
			t.Errorf("n = %d, want %d", n, len(data))
		}
	})
}

func TestLinkedListBuffer_ReadFrom_PanicNegativeRead(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on negative read count")
		}
	}()

	ll := &LinkedListBuffer{}
	ll.ReadFrom(negativeReader{})
}

// =============================================================================
// Method: WriteTo()
// =============================================================================

func TestLinkedListBuffer_WriteTo(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("hello"))

		var dst bytes.Buffer
		n, err := ll.WriteTo(&dst)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if n != 5 {
			t.Errorf("n = %d, want 5", n)
		}
		if !bytes.Equal(dst.Bytes(), []byte("hello")) {
			t.Errorf("data = %q, want %q", dst.Bytes(), "hello")
		}
		if !ll.IsEmpty() {
			t.Error("buffer should be empty after WriteTo")
		}
	})

	t.Run("empty", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		var dst bytes.Buffer
		n, err := ll.WriteTo(&dst)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if n != 0 {
			t.Errorf("n = %d, want 0", n)
		}
	})

	t.Run("error", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("data"))

		_, err := ll.WriteTo(llErrorWriter{})
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("short_write", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("hello"))

		_, err := ll.WriteTo(shortWriter{})
		if err != io.ErrShortWrite {
			t.Errorf("err = %v, want ErrShortWrite", err)
		}
		// Remaining should be pushed back
		if ll.IsEmpty() {
			t.Error("remaining data should be pushed back")
		}
	})

	t.Run("multi_node", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("A"))
		ll.PushBack([]byte("B"))
		ll.PushBack([]byte("C"))

		var dst bytes.Buffer
		n, err := ll.WriteTo(&dst)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if n != 3 {
			t.Errorf("n = %d, want 3", n)
		}
		if !bytes.Equal(dst.Bytes(), []byte("ABC")) {
			t.Errorf("data = %q, want %q", dst.Bytes(), "ABC")
		}
	})
}

// =============================================================================
// Method: Len()
// =============================================================================

func TestLinkedListBuffer_Len(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *LinkedListBuffer
		wantLen int
	}{
		{
			name:    "empty",
			setup:   func() *LinkedListBuffer { return &LinkedListBuffer{} },
			wantLen: 0,
		},
		{
			name: "with_nodes",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("A"))
				ll.PushBack([]byte("B"))
				ll.PushBack([]byte("C"))
				return ll
			},
			wantLen: 3,
		},
		{
			name: "after_pop",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("A"))
				ll.PushBack([]byte("B"))
				ll.PushBack([]byte("C"))
				ll.FreeNode(ll.Pop())
				return ll
			},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := tt.setup()
			if ll.Len() != tt.wantLen {
				t.Errorf("Len = %d, want %d", ll.Len(), tt.wantLen)
			}
		})
	}
}

// =============================================================================
// Method: Buffered()
// =============================================================================

func TestLinkedListBuffer_Buffered(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() *LinkedListBuffer
		wantBufed int
	}{
		{
			name:      "empty",
			setup:     func() *LinkedListBuffer { return &LinkedListBuffer{} },
			wantBufed: 0,
		},
		{
			name: "with_data",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("hello"))  // 5
				ll.PushBack([]byte("world!")) // 6
				return ll
			},
			wantBufed: 11,
		},
		{
			name: "after_pop",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("AAAA")) // 4
				ll.PushBack([]byte("BB"))   // 2
				ll.FreeNode(ll.Pop())       // -4
				return ll
			},
			wantBufed: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := tt.setup()
			if ll.Buffered() != tt.wantBufed {
				t.Errorf("Buffered = %d, want %d", ll.Buffered(), tt.wantBufed)
			}
		})
	}
}

// =============================================================================
// Method: IsEmpty()
// =============================================================================

func TestLinkedListBuffer_IsEmpty(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() *LinkedListBuffer
		wantEmpty bool
	}{
		{
			name:      "new",
			setup:     func() *LinkedListBuffer { return &LinkedListBuffer{} },
			wantEmpty: true,
		},
		{
			name: "with_data",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("data"))
				return ll
			},
			wantEmpty: false,
		},
		{
			name: "after_reset",
			setup: func() *LinkedListBuffer {
				ll := &LinkedListBuffer{}
				ll.PushBack([]byte("data"))
				ll.Reset()
				return ll
			},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := tt.setup()
			if ll.IsEmpty() != tt.wantEmpty {
				t.Errorf("IsEmpty = %v, want %v", ll.IsEmpty(), tt.wantEmpty)
			}
		})
	}
}

// =============================================================================
// Method: Reset()
// =============================================================================

func TestLinkedListBuffer_Reset(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("A"))
		ll.PushBack([]byte("B"))
		ll.PushBack([]byte("C"))

		ll.Reset()

		if !ll.IsEmpty() {
			t.Error("after Reset, should be empty")
		}
		if ll.Len() != 0 {
			t.Errorf("Len = %d, want 0", ll.Len())
		}
		if ll.Buffered() != 0 {
			t.Errorf("Buffered = %d, want 0", ll.Buffered())
		}
	})

	t.Run("already_empty", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.Reset() // Should not panic
		if !ll.IsEmpty() {
			t.Error("should still be empty")
		}
	})

	t.Run("multi_reset", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		for i := 0; i < 3; i++ {
			ll.PushBack([]byte("data"))
			ll.Reset()
			if !ll.IsEmpty() {
				t.Errorf("reset %d: should be empty", i)
			}
		}
	})

	t.Run("reusable", func(t *testing.T) {
		ll := &LinkedListBuffer{}
		ll.PushBack([]byte("first"))
		ll.Reset()
		ll.PushBack([]byte("second"))

		buf := make([]byte, 10)
		n, _ := ll.Read(buf)
		if string(buf[:n]) != "second" {
			t.Errorf("data = %q, want %q", buf[:n], "second")
		}
	})
}

// =============================================================================
// Integration: Producer-Consumer Workflow
// =============================================================================

func TestLinkedListBuffer_Workflow_ProducerConsumer(t *testing.T) {
	ll := &LinkedListBuffer{}

	// Producer writes data
	ll.PushBack([]byte("chunk1"))
	ll.PushBack([]byte("chunk2"))
	ll.PushBack([]byte("chunk3"))

	if ll.Len() != 3 {
		t.Fatalf("after produce, Len = %d, want 3", ll.Len())
	}
	if ll.Buffered() != 18 {
		t.Fatalf("after produce, Buffered = %d, want 18", ll.Buffered())
	}

	// Consumer reads data
	var dst bytes.Buffer
	n, err := ll.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo err = %v", err)
	}
	if n != 18 {
		t.Errorf("WriteTo n = %d, want 18", n)
	}
	if !bytes.Equal(dst.Bytes(), []byte("chunk1chunk2chunk3")) {
		t.Errorf("data = %q", dst.Bytes())
	}

	// Buffer should be empty
	if !ll.IsEmpty() {
		t.Error("after consume, should be empty")
	}

	// Reset and reuse
	ll.Reset()
	ll.PushBack([]byte("reused"))
	if ll.Buffered() != 6 {
		t.Errorf("after reuse, Buffered = %d, want 6", ll.Buffered())
	}
}
