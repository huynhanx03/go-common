package forge

import (
	"bytes"
	"testing"
)

func TestRecordBatchRoundTrip(t *testing.T) {
	batch := &RecordBatch{
		Compression: CompressionNone,
		Timestamp:   1000000,
		MaxTimestamp: 1000500,
		Records: []Record{
			{
				TimestampDelta: 0,
				OffsetDelta:    0,
				Key:            []byte("user:123"),
				Value:          []byte(`{"action":"login"}`),
				Headers: []Header{
					{Key: []byte("source"), Value: []byte("web")},
				},
			},
			{
				TimestampDelta: 100,
				OffsetDelta:    1,
				Key:            nil, // null key
				Value:          []byte("hello world"),
			},
			{
				TimestampDelta: 500,
				OffsetDelta:    2,
				Key:            []byte{}, // empty key
				Value:          []byte(""),
			},
		},
		RecordCount: 3,
		BaseOffset:  42,
	}

	encoded, err := EncodeBatch(batch, nil)
	if err != nil {
		t.Fatalf("EncodeBatch: %v", err)
	}

	decoded, err := DecodeBatch(encoded)
	if err != nil {
		t.Fatalf("DecodeBatch: %v", err)
	}

	if decoded.BaseOffset != 42 {
		t.Errorf("BaseOffset = %d, want 42", decoded.BaseOffset)
	}
	if decoded.RecordCount != 3 {
		t.Errorf("RecordCount = %d, want 3", decoded.RecordCount)
	}
	if decoded.Timestamp != 1000000 {
		t.Errorf("Timestamp = %d, want 1000000", decoded.Timestamp)
	}
	if decoded.MaxTimestamp != 1000500 {
		t.Errorf("MaxTimestamp = %d, want 1000500", decoded.MaxTimestamp)
	}

	// Record 0: normal key+value+headers.
	r0 := decoded.Records[0]
	if !bytes.Equal(r0.Key, []byte("user:123")) {
		t.Errorf("r0.Key = %q, want %q", r0.Key, "user:123")
	}
	if !bytes.Equal(r0.Value, []byte(`{"action":"login"}`)) {
		t.Errorf("r0.Value mismatch")
	}
	if len(r0.Headers) != 1 || !bytes.Equal(r0.Headers[0].Key, []byte("source")) {
		t.Errorf("r0.Headers mismatch")
	}

	// Record 1: null key.
	r1 := decoded.Records[1]
	if r1.Key != nil {
		t.Errorf("r1.Key = %v, want nil", r1.Key)
	}
	if !bytes.Equal(r1.Value, []byte("hello world")) {
		t.Errorf("r1.Value mismatch")
	}

	// Record 2: empty key + empty value.
	r2 := decoded.Records[2]
	if r2.Key == nil || len(r2.Key) != 0 {
		t.Errorf("r2.Key = %v, want empty slice", r2.Key)
	}
}

func TestBatchSize(t *testing.T) {
	batch := &RecordBatch{
		Compression: CompressionNone,
		Timestamp:   999,
		MaxTimestamp: 999,
		Records:     []Record{{Value: []byte("test")}},
		RecordCount: 1,
	}

	encoded, err := EncodeBatch(batch, nil)
	if err != nil {
		t.Fatal(err)
	}

	size, err := BatchSize(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if size != len(encoded) {
		t.Errorf("BatchSize = %d, want %d", size, len(encoded))
	}
}

func TestDecodeBatchCorrupt(t *testing.T) {
	_, err := DecodeBatch([]byte{0, 1, 2})
	if err != ErrCorruptBatch {
		t.Errorf("expected ErrCorruptBatch, got %v", err)
	}
}

func TestDecodeBatchCRCMismatch(t *testing.T) {
	batch := &RecordBatch{
		Compression: CompressionNone,
		Timestamp:   1,
		MaxTimestamp: 1,
		Records:     []Record{{Value: []byte("x")}},
		RecordCount: 1,
	}
	encoded, _ := EncodeBatch(batch, nil)

	// Corrupt a byte in the records area.
	encoded[len(encoded)-1] ^= 0xFF

	_, err := DecodeBatch(encoded)
	if err != ErrChecksumMismatch {
		t.Errorf("expected ErrChecksumMismatch, got %v", err)
	}
}

func TestLZ4RoundTrip(t *testing.T) {
	batch := &RecordBatch{
		Compression: CompressionLZ4,
		Timestamp:   999,
		MaxTimestamp: 999,
		RecordCount: 3,
		Records: []Record{
			{Key: []byte("a"), Value: []byte("hello world hello world hello world")},
			{Key: []byte("b"), Value: []byte("repeating data repeating data repeating data")},
			{Key: nil, Value: []byte("compressed message")},
		},
	}

	encoded, err := EncodeBatch(batch, nil)
	if err != nil {
		t.Fatalf("EncodeBatch LZ4: %v", err)
	}

	decoded, err := DecodeBatch(encoded)
	if err != nil {
		t.Fatalf("DecodeBatch LZ4: %v", err)
	}

	if decoded.Compression != CompressionLZ4 {
		t.Errorf("compression = %d, want %d", decoded.Compression, CompressionLZ4)
	}
	if len(decoded.Records) != 3 {
		t.Fatalf("record count = %d, want 3", len(decoded.Records))
	}
	if !bytes.Equal(decoded.Records[0].Value, []byte("hello world hello world hello world")) {
		t.Error("LZ4 record 0 value mismatch")
	}
	if decoded.Records[2].Key != nil {
		t.Error("LZ4 record 2 key should be nil")
	}
}

func TestLZ4ProduceConsume(t *testing.T) {
	dir := t.TempDir()
	b, _ := NewBroker(dir)
	defer b.Close()

	p, _ := b.NewProducer("lz4-topic", WithCompression(CompressionLZ4))
	for i := 0; i < 20; i++ {
		p.Send(nil, []byte("compressible data compressible data compressible data"), nil)
	}
	p.Close()

	c, _ := b.NewConsumer("g", "lz4-topic")
	records, err := c.Poll(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 20 {
		t.Fatalf("LZ4 consume: got %d, want 20", len(records))
	}
	if !bytes.Equal(records[0].Value, []byte("compressible data compressible data compressible data")) {
		t.Error("LZ4 consume: value mismatch")
	}
}

func TestEncodeBatchReuseDst(t *testing.T) {
	batch := &RecordBatch{
		Records:     []Record{{Value: []byte("reuse")}},
		RecordCount: 1,
	}

	// Provide a large enough dst buffer.
	dst := make([]byte, 1024)
	encoded, err := EncodeBatch(batch, dst)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeBatch(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Records[0].Value, []byte("reuse")) {
		t.Error("reuse dst: value mismatch")
	}
}
