package forge

import (
	"bytes"
	"errors"
	"testing"
)

func TestCompressorRoundTripsPayloadsAndReusesCallerBuffer(t *testing.T) {
	input := bytes.Repeat([]byte("compressible-event-payload"), 64)
	compressor := compressorFor(CompressionLZ4)
	compressed, err := compressor.Compress(nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) >= len(input) {
		t.Fatalf("compressible payload grew from %d to %d bytes", len(input), len(compressed))
	}
	decoded, err := compressor.Decompress(nil, compressed)
	if err != nil || !bytes.Equal(decoded, input) {
		t.Fatalf("decompressed payload mismatch: err=%v", err)
	}

	reusable := make([]byte, 0, 256)
	compressed, err = compressor.Compress(reusable, input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = compressor.Decompress(make([]byte, 0, len(input)), compressed)
	if err != nil || !bytes.Equal(decoded, input) {
		t.Fatalf("reused-buffer payload mismatch: err=%v", err)
	}
}

func TestCompressorRejectsMalformedPayloads(t *testing.T) {
	compressor := lz4Compressor{}
	for _, input := range [][]byte{nil, make([]byte, maxEncodedBatchBytes+1)} {
		if _, err := compressor.Decompress(nil, input); !errors.Is(err, ErrCorruptBatch) {
			t.Fatalf("invalid compressed payload length %d returned %v", len(input), err)
		}
	}
	if _, err := compressor.Decompress(nil, []byte{0xff}); err == nil {
		t.Fatal("malformed LZ4 block was accepted")
	}
}

func TestNoCompressionPreservesPayloadAndUnknownCompressionFallsBackToNone(t *testing.T) {
	input := []byte("opaque event")
	for _, compressor := range []Compressor{noneCompressor{}, compressorFor(0xff)} {
		encoded, err := compressor.Compress([]byte("unused"), input)
		if err != nil || !bytes.Equal(encoded, input) {
			t.Fatalf("passthrough encode = (%q, %v)", encoded, err)
		}
		decoded, err := compressor.Decompress(nil, encoded)
		if err != nil || !bytes.Equal(decoded, input) {
			t.Fatalf("passthrough decode = (%q, %v)", decoded, err)
		}
	}
}
