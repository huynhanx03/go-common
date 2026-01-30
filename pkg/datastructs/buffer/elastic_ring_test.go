package buffer

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// =============================================================================
// Interface Compliance (compile-time checks)
// =============================================================================

var _ io.Reader = (*ElasticRing)(nil)
var _ io.Writer = (*ElasticRing)(nil)
var _ io.ReaderFrom = (*ElasticRing)(nil)
var _ io.WriterTo = (*ElasticRing)(nil)
var _ io.ByteReader = (*ElasticRing)(nil)
var _ io.ByteWriter = (*ElasticRing)(nil)
var _ io.StringWriter = (*ElasticRing)(nil)

// =============================================================================
// Method: Done()
// =============================================================================

func TestElasticRing_Done(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *ElasticRing
	}{
		{
			name: "after_write",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
		},
		{
			name: "nil_ring",
			setup: func() *ElasticRing {
				return &ElasticRing{} // ring is nil
			},
		},
		{
			name: "after_reset",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				er.Reset()
				return er
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := tt.setup()
			er.Done() // should not panic
			if er.ring != nil {
				t.Error("ring should be nil after Done")
			}
		})
	}
}

func TestElasticRing_Done_Double(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("data"))
	er.Done()
	er.Done() // should not panic
	if er.ring != nil {
		t.Error("ring should be nil after double Done")
	}
}

func TestElasticRing_Done_Reuse(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("first"))
	er.Done()

	// Should allocate new buffer from pool
	n, err := er.Write([]byte("second"))
	if err != nil {
		t.Fatalf("Write after Done error: %v", err)
	}
	if n != 6 {
		t.Errorf("n = %d, want 6", n)
	}
	if !bytes.Equal(er.Bytes(), []byte("second")) {
		t.Errorf("Bytes = %q, want %q", er.Bytes(), "second")
	}
}

// =============================================================================
// Method: Peek()
// =============================================================================

func TestElasticRing_Peek(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *ElasticRing
		n        int
		wantHead []byte
		wantTail []byte
	}{
		{
			name: "happy_path",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
			n:        5,
			wantHead: []byte("hello"),
			wantTail: nil,
		},
		{
			name: "nil_ring",
			setup: func() *ElasticRing {
				return &ElasticRing{}
			},
			n:        5,
			wantHead: nil,
			wantTail: nil,
		},
		{
			name: "zero_n",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
			n:        0,
			wantHead: []byte("hello"),
			wantTail: nil,
		},
		{
			name: "large_n",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hi"))
				return er
			},
			n:        100,
			wantHead: []byte("hi"),
			wantTail: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := tt.setup()
			head, tail := er.Peek(tt.n)
			if !bytes.Equal(head, tt.wantHead) {
				t.Errorf("head = %q, want %q", head, tt.wantHead)
			}
			if !bytes.Equal(tail, tt.wantTail) {
				t.Errorf("tail = %q, want %q", tail, tt.wantTail)
			}
		})
	}
}

func TestElasticRing_Peek_NoAdvance(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))

	// Peek should not consume data
	er.Peek(3)
	if er.Buffered() != 5 {
		t.Errorf("Buffered after Peek = %d, want 5", er.Buffered())
	}

	// Data should still be readable
	buf := make([]byte, 5)
	n, _ := er.Read(buf)
	if n != 5 || string(buf) != "hello" {
		t.Error("data should still be readable after Peek")
	}
}

// =============================================================================
// Method: Discard()
// =============================================================================

func TestElasticRing_Discard(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *ElasticRing
		n       int
		wantN   int
		wantErr error
	}{
		{
			name: "happy_path",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
			n:       3,
			wantN:   3,
			wantErr: nil,
		},
		{
			name: "nil_ring",
			setup: func() *ElasticRing {
				return &ElasticRing{}
			},
			n:       5,
			wantN:   0,
			wantErr: ErrRingEmpty,
		},
		{
			name: "zero_n",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
			n:       0,
			wantN:   0,
			wantErr: nil,
		},
		{
			name: "large_n",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hi"))
				return er
			},
			n:       100,
			wantN:   2,
			wantErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := tt.setup()
			n, err := er.Discard(tt.n)
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestElasticRing_Discard_PoolReturn(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))
	er.Discard(5) // discard all

	// Ring should be returned to pool
	if er.ring != nil {
		t.Error("ring should be nil after discarding all data")
	}
}

// =============================================================================
// Method: Read()
// =============================================================================

func TestElasticRing_Read(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *ElasticRing
		bufSize int
		wantN   int
		wantErr error
	}{
		{
			name: "happy_path",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
			bufSize: 10,
			wantN:   5,
			wantErr: nil,
		},
		{
			name: "nil_ring",
			setup: func() *ElasticRing {
				return &ElasticRing{}
			},
			bufSize: 10,
			wantN:   0,
			wantErr: ErrRingEmpty,
		},
		{
			name: "partial_read",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello world"))
				return er
			},
			bufSize: 5,
			wantN:   5,
			wantErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := tt.setup()
			buf := make([]byte, tt.bufSize)
			n, err := er.Read(buf)
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestElasticRing_Read_NilBuf(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))

	n, err := er.Read(nil)
	if n != 0 || err != nil {
		t.Errorf("Read(nil) = %d, %v; want 0, nil", n, err)
	}
}

func TestElasticRing_Read_EmptyBuf(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))

	n, err := er.Read([]byte{})
	if n != 0 || err != nil {
		t.Errorf("Read(empty) = %d, %v; want 0, nil", n, err)
	}
}

func TestElasticRing_Read_PoolReturn(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))

	buf := make([]byte, 10)
	er.Read(buf) // reads all

	// Ring should be returned to pool
	if er.ring != nil {
		t.Error("ring should be nil after reading all data")
	}
}

// =============================================================================
// Method: ReadByte()
// =============================================================================

func TestElasticRing_ReadByte(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("ABC"))

	b, err := er.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte error: %v", err)
	}
	if b != 'A' {
		t.Errorf("byte = %c, want A", b)
	}

	b, err = er.ReadByte()
	if err != nil || b != 'B' {
		t.Errorf("second byte = %c, want B", b)
	}
}

func TestElasticRing_ReadByte_NilRing(t *testing.T) {
	er := &ElasticRing{}
	_, err := er.ReadByte()
	if !errors.Is(err, ErrRingEmpty) {
		t.Errorf("err = %v, want ErrRingEmpty", err)
	}
}

func TestElasticRing_ReadByte_PoolReturn(t *testing.T) {
	er := &ElasticRing{}
	er.WriteByte('X')

	er.ReadByte() // reads last byte

	// Ring should be returned to pool
	if er.ring != nil {
		t.Error("ring should be nil after reading last byte")
	}
}

// =============================================================================
// Method: Write()
// =============================================================================

func TestElasticRing_Write(t *testing.T) {
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
			er := &ElasticRing{}
			n, err := er.Write(tt.input)
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestElasticRing_Write_LazyAlloc(t *testing.T) {
	er := &ElasticRing{}
	if er.ring != nil {
		t.Error("ring should be nil before first write")
	}

	er.Write([]byte("hello"))
	if er.ring == nil {
		t.Error("ring should be allocated after write")
	}
}

func TestElasticRing_Write_NoAllocOnEmpty(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte{}) // empty write
	if er.ring != nil {
		t.Error("ring should not allocate on empty write")
	}

	er.Write(nil) // nil write
	if er.ring != nil {
		t.Error("ring should not allocate on nil write")
	}
}

func TestElasticRing_Write_Large(t *testing.T) {
	er := &ElasticRing{}
	data := make([]byte, 1<<20) // 1MB
	for i := range data {
		data[i] = byte(i % 256)
	}

	n, err := er.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
}

func TestElasticRing_Write_Multiple(t *testing.T) {
	er := &ElasticRing{}
	for i := 0; i < 5; i++ {
		er.Write([]byte("X"))
	}
	if !bytes.Equal(er.Bytes(), []byte("XXXXX")) {
		t.Errorf("Bytes = %q, want XXXXX", er.Bytes())
	}
}

// =============================================================================
// Method: WriteByte()
// =============================================================================

func TestElasticRing_WriteByte(t *testing.T) {
	er := &ElasticRing{}
	err := er.WriteByte('A')
	if err != nil {
		t.Fatalf("WriteByte error: %v", err)
	}
	if !bytes.Equal(er.Bytes(), []byte("A")) {
		t.Errorf("Bytes = %q, want A", er.Bytes())
	}
}

func TestElasticRing_WriteByte_LazyAlloc(t *testing.T) {
	er := &ElasticRing{}
	if er.ring != nil {
		t.Error("ring should be nil before first WriteByte")
	}

	er.WriteByte('X')
	if er.ring == nil {
		t.Error("ring should be allocated after WriteByte")
	}
}

func TestElasticRing_WriteByte_Multiple(t *testing.T) {
	er := &ElasticRing{}
	for i := 0; i < 3; i++ {
		er.WriteByte('A' + byte(i))
	}
	if !bytes.Equal(er.Bytes(), []byte("ABC")) {
		t.Errorf("Bytes = %q, want ABC", er.Bytes())
	}
}

// =============================================================================
// Method: WriteString()
// =============================================================================

func TestElasticRing_WriteString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantN   int
		wantErr bool
	}{
		{"valid", "hello", 5, false},
		{"empty", "", 0, false},
		{"unicode", "你好", 6, false}, // 2 unicode chars = 6 bytes
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := &ElasticRing{}
			n, err := er.WriteString(tt.input)
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestElasticRing_WriteString_NoAllocOnEmpty(t *testing.T) {
	er := &ElasticRing{}
	er.WriteString("")
	if er.ring != nil {
		t.Error("ring should not allocate on empty WriteString")
	}
}

// =============================================================================
// Method: Buffered()
// =============================================================================

func TestElasticRing_Buffered(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *ElasticRing
		want  int
	}{
		{
			name: "after_write",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
			want: 5,
		},
		{
			name: "nil_ring",
			setup: func() *ElasticRing {
				return &ElasticRing{}
			},
			want: 0,
		},
		{
			name: "after_reset",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				er.Reset()
				return er
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := tt.setup()
			if got := er.Buffered(); got != tt.want {
				t.Errorf("Buffered = %d, want %d", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Method: Available()
// =============================================================================

func TestElasticRing_Available(t *testing.T) {
	// Nil ring
	er := &ElasticRing{}
	if er.Available() != 0 {
		t.Errorf("Available on nil = %d, want 0", er.Available())
	}

	// After write
	er.Write([]byte("hello"))
	initial := er.Available()
	if initial <= 0 {
		t.Error("Available after write should be > 0")
	}

	// Read should increase
	buf := make([]byte, 3)
	er.Read(buf)
	if er.Available() <= initial {
		t.Error("Available should increase after read")
	}
}

// =============================================================================
// Method: Len()
// =============================================================================

func TestElasticRing_Len(t *testing.T) {
	// Nil ring
	er := &ElasticRing{}
	if er.Len() != 0 {
		t.Errorf("Len on nil = %d, want 0", er.Len())
	}

	// After write
	er.Write([]byte("hello"))
	if er.Len() <= 0 {
		t.Error("Len after alloc should be > 0")
	}
}

// =============================================================================
// Method: Cap()
// =============================================================================

func TestElasticRing_Cap(t *testing.T) {
	// Nil ring
	er := &ElasticRing{}
	if er.Cap() != 0 {
		t.Errorf("Cap on nil = %d, want 0", er.Cap())
	}

	// After write
	er.Write([]byte("hello"))
	if er.Cap() <= 0 {
		t.Error("Cap after alloc should be > 0")
	}
}

// =============================================================================
// Method: Bytes()
// =============================================================================

func TestElasticRing_Bytes(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *ElasticRing
		want  []byte
	}{
		{
			name: "after_write",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hello"))
				return er
			},
			want: []byte("hello"),
		},
		{
			name: "nil_ring",
			setup: func() *ElasticRing {
				return &ElasticRing{}
			},
			want: nil,
		},
		{
			name: "multiple_writes",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("A"))
				er.Write([]byte("B"))
				er.Write([]byte("C"))
				return er
			},
			want: []byte("ABC"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := tt.setup()
			got := er.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Bytes = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestElasticRing_Bytes_IsCopy(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))

	result := er.Bytes()
	result[0] = 'X' // modify returned slice

	// Original should be unchanged
	expected := []byte("hello")
	if !bytes.Equal(er.Bytes(), expected) {
		t.Error("Bytes should return a copy, original modified")
	}
}

// =============================================================================
// Method: ReadFrom()
// =============================================================================

func TestElasticRing_ReadFrom(t *testing.T) {
	er := &ElasticRing{}
	r := strings.NewReader("hello world")

	n, err := er.ReadFrom(r)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != 11 {
		t.Errorf("n = %d, want 11", n)
	}
	if !bytes.Equal(er.Bytes(), []byte("hello world")) {
		t.Errorf("Bytes = %q, want %q", er.Bytes(), "hello world")
	}
}

func TestElasticRing_ReadFrom_Empty(t *testing.T) {
	er := &ElasticRing{}
	r := strings.NewReader("")

	n, err := er.ReadFrom(r)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) {
	return 0, errors.New("read error")
}

func TestElasticRing_ReadFrom_Error(t *testing.T) {
	er := &ElasticRing{}
	_, err := er.ReadFrom(errReader{})
	if err == nil {
		t.Error("expected error from ReadFrom")
	}
}

func TestElasticRing_ReadFrom_Large(t *testing.T) {
	er := &ElasticRing{}
	data := make([]byte, 100*1024) // 100KB
	r := bytes.NewReader(data)

	n, err := er.ReadFrom(r)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != int64(len(data)) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
}

func TestElasticRing_ReadFrom_LazyAlloc(t *testing.T) {
	er := &ElasticRing{}
	r := strings.NewReader("hi")

	er.ReadFrom(r)
	if er.ring == nil {
		t.Error("ring should be allocated after ReadFrom")
	}
}

// =============================================================================
// Method: WriteTo()
// =============================================================================

func TestElasticRing_WriteTo(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))

	var dst bytes.Buffer
	n, err := er.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if dst.String() != "hello" {
		t.Errorf("dst = %q, want hello", dst.String())
	}
}

func TestElasticRing_WriteTo_NilRing(t *testing.T) {
	er := &ElasticRing{}
	var dst bytes.Buffer

	n, err := er.WriteTo(&dst)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write error")
}

func TestElasticRing_WriteTo_Error(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("data"))

	_, err := er.WriteTo(errWriter{})
	if err == nil {
		t.Error("expected error from WriteTo")
	}
}

func TestElasticRing_WriteTo_PoolReturn(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))

	var dst bytes.Buffer
	er.WriteTo(&dst)

	// Ring should be returned to pool
	if er.ring != nil {
		t.Error("ring should be nil after WriteTo all data")
	}
}

// =============================================================================
// Method: IsFull()
// =============================================================================

func TestElasticRing_IsFull(t *testing.T) {
	// Nil ring
	er := &ElasticRing{}
	if er.IsFull() {
		t.Error("nil ring should not be full")
	}

	// After write but not full
	er.Write([]byte("hi"))
	if er.IsFull() {
		t.Error("partial buffer should not be full")
	}
}

// =============================================================================
// Method: IsEmpty()
// =============================================================================

func TestElasticRing_IsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *ElasticRing
		want  bool
	}{
		{
			name: "nil_ring",
			setup: func() *ElasticRing {
				return &ElasticRing{}
			},
			want: true,
		},
		{
			name: "after_write",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hi"))
				return er
			},
			want: false,
		},
		{
			name: "after_reset",
			setup: func() *ElasticRing {
				er := &ElasticRing{}
				er.Write([]byte("hi"))
				er.Reset()
				return er
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := tt.setup()
			if got := er.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Method: Reset()
// =============================================================================

func TestElasticRing_Reset(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("hello"))
	er.Reset()

	if !er.IsEmpty() {
		t.Error("buffer should be empty after Reset")
	}
	if er.Buffered() != 0 {
		t.Error("Buffered should be 0 after Reset")
	}
	// Ring should still be allocated (Reset != Done)
	if er.ring == nil {
		t.Error("ring should not be nil after Reset")
	}
}

func TestElasticRing_Reset_NilRing(t *testing.T) {
	er := &ElasticRing{}
	er.Reset() // should not panic
}

func TestElasticRing_Reset_Reuse(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("old"))
	er.Reset()
	er.Write([]byte("new"))

	if !bytes.Equal(er.Bytes(), []byte("new")) {
		t.Errorf("Bytes = %q, want new", er.Bytes())
	}
}

func TestElasticRing_Reset_Double(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("data"))
	er.Reset()
	er.Reset() // should not panic
}

// =============================================================================
// Sequence/Integration Tests
// =============================================================================

func TestElasticRing_Workflow_WriteRead(t *testing.T) {
	er := &ElasticRing{}
	data := []byte("hello world")

	n, _ := er.Write(data)
	if n != len(data) {
		t.Fatalf("Write n = %d, want %d", n, len(data))
	}

	buf := make([]byte, 20)
	n, _ = er.Read(buf)
	if n != len(data) {
		t.Fatalf("Read n = %d, want %d", n, len(data))
	}
	if !bytes.Equal(buf[:n], data) {
		t.Errorf("Read data = %q, want %q", buf[:n], data)
	}
}

func TestElasticRing_Workflow_PoolReuse(t *testing.T) {
	er := &ElasticRing{}

	// First use
	er.Write([]byte("first"))
	buf := make([]byte, 10)
	er.Read(buf) // drains buffer, returns to pool

	// Second use should get buffer from pool
	er.Write([]byte("second"))
	if er.Buffered() != 6 {
		t.Errorf("Buffered = %d, want 6", er.Buffered())
	}
}

func TestElasticRing_Workflow_MixedOps(t *testing.T) {
	er := &ElasticRing{}
	er.Write([]byte("ABCDEFGH"))

	// Peek
	head, _ := er.Peek(3)
	if string(head) != "ABC" {
		t.Errorf("Peek = %q, want ABC", head)
	}

	// Discard
	er.Discard(2) // skip AB

	// Read
	buf := make([]byte, 3)
	er.Read(buf) // read CDE
	if string(buf) != "CDE" {
		t.Errorf("Read = %q, want CDE", buf)
	}
}

func TestElasticRing_Workflow_StreamCopy(t *testing.T) {
	er := &ElasticRing{}
	src := strings.NewReader("stream data")

	er.ReadFrom(src)

	var dst bytes.Buffer
	n, err := er.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	if n != 11 {
		t.Errorf("n = %d, want 11", n)
	}
	if dst.String() != "stream data" {
		t.Errorf("dst = %q, want 'stream data'", dst.String())
	}
}
