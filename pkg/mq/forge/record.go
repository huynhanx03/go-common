package forge

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
)

const (
	batchHeaderSize = 35 // BaseOffset(8)+BatchLength(4)+RecordCount(2)+Compression(1)+CRC(4)+Timestamp(8)+MaxTimestamp(8)

	CompressionNone = uint8(0)
	CompressionLZ4  = uint8(1)
)

var (
	crc32cTable = crc32.MakeTable(crc32.Castagnoli)

	// recBufPool reuses record encoding buffers to reduce GC pressure on hot path.
	recBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, 0, defaultRecordBufCap)
			return &b
		},
	}
)

// Header is a key-value pair attached to a Record.
type Header struct {
	Key   []byte
	Value []byte
}

// Record represents a single message in a batch.
type Record struct {
	TimestampDelta int64
	OffsetDelta    int64
	Key            []byte
	Value          []byte
	Headers        []Header
}

// RecordBatch is a group of records written atomically to the log.
type RecordBatch struct {
	BaseOffset  uint64
	RecordCount uint16
	Compression uint8
	Timestamp   int64
	MaxTimestamp int64
	Records     []Record
}

// EncodeBatch serializes a RecordBatch into dst (reused if large enough).
// Returns the encoded bytes.
func EncodeBatch(b *RecordBatch, dst []byte) ([]byte, error) {
	if b.RecordCount == 0 {
		return nil, ErrEmptyBatch
	}

	// Encode all records into a pooled buffer.
	bufPtr := recBufPool.Get().(*[]byte)
	recBuf := (*bufPtr)[:0]
	for i := range b.Records {
		recBuf = appendRecord(recBuf, &b.Records[i])
	}

	// Return the pooled buffer BEFORE compression may replace recBuf.
	// This prevents putting a compressed (non-pooled) buffer back into the pool.
	var payload []byte

	// Compress records if requested.
	actualCompression := b.Compression
	if b.Compression != CompressionNone {
		comp := compressorFor(b.Compression)
		compressed, err := comp.Compress(nil, recBuf)
		if err != nil {
			if errors.Is(err, ErrIncompressible) {
				// Incompressible data — fall back to no compression.
				actualCompression = CompressionNone
				payload = recBuf
			} else {
				*bufPtr = recBuf
				recBufPool.Put(bufPtr)
				return nil, err
			}
		} else {
			payload = compressed
			// Return original pooled buffer (not the compressed one).
			*bufPtr = recBuf
			recBufPool.Put(bufPtr)
		}
	} else {
		payload = recBuf
		// Defer pool return — payload references recBuf, will be copied below.
	}

	batchLen := uint32(2 + 1 + 4 + 8 + 8 + len(payload)) // everything after BatchLength field
	needed := batchHeaderSize + len(payload)

	if cap(dst) >= needed {
		dst = dst[:needed]
	} else {
		dst = make([]byte, needed)
	}

	// Fixed header fields.
	off := 0
	binary.BigEndian.PutUint64(dst[off:], b.BaseOffset)
	off += 8
	binary.BigEndian.PutUint32(dst[off:], batchLen)
	off += 4
	binary.BigEndian.PutUint16(dst[off:], b.RecordCount)
	off += 2
	dst[off] = actualCompression
	off++

	crcPos := off // placeholder for CRC
	off += 4

	binary.BigEndian.PutUint64(dst[off:], uint64(b.Timestamp))
	off += 8
	binary.BigEndian.PutUint64(dst[off:], uint64(b.MaxTimestamp))
	off += 8

	copy(dst[off:], payload)

	// Return uncompressed pooled buffer after copy is done.
	// This covers: CompressionNone, and LZ4 incompressible fallback.
	if actualCompression == CompressionNone {
		*bufPtr = recBuf
		recBufPool.Put(bufPtr)
	}

	// CRC32C covers from Timestamp to end of batch.
	crc := crc32.Checksum(dst[crcPos+4:needed], crc32cTable)
	binary.BigEndian.PutUint32(dst[crcPos:], crc)

	return dst, nil
}

// DecodeBatch deserializes a RecordBatch from raw bytes.
func DecodeBatch(data []byte) (*RecordBatch, error) {
	if len(data) < batchHeaderSize {
		return nil, ErrCorruptBatch
	}

	off := 0
	b := &RecordBatch{}
	b.BaseOffset = binary.BigEndian.Uint64(data[off:])
	off += 8
	batchLen := binary.BigEndian.Uint32(data[off:])
	off += 4
	b.RecordCount = binary.BigEndian.Uint16(data[off:])
	off += 2
	b.Compression = data[off]
	off++

	storedCRC := binary.BigEndian.Uint32(data[off:])
	off += 4

	totalLen := 12 + int(batchLen)
	if len(data) < totalLen {
		return nil, ErrCorruptBatch
	}

	// Verify CRC32C: Timestamp to end.
	computed := crc32.Checksum(data[off:totalLen], crc32cTable)
	if computed != storedCRC {
		return nil, ErrChecksumMismatch
	}

	b.Timestamp = int64(binary.BigEndian.Uint64(data[off:]))
	off += 8
	b.MaxTimestamp = int64(binary.BigEndian.Uint64(data[off:]))
	off += 8

	recData := data[off:totalLen]

	// Decompress if needed.
	if b.Compression != CompressionNone {
		comp := compressorFor(b.Compression)
		decompressed, err := comp.Decompress(nil, recData)
		if err != nil {
			return nil, err
		}
		recData = decompressed
	}

	b.Records = make([]Record, 0, b.RecordCount)
	for i := 0; i < int(b.RecordCount); i++ {
		rec, n, err := decodeRecord(recData)
		if err != nil {
			return nil, err
		}
		b.Records = append(b.Records, rec)
		recData = recData[n:]
	}

	return b, nil
}

// ParseBatchHeader extracts BaseOffset and RecordCount from encoded batch bytes
// without decoding records, verifying CRC, or decompressing. Used for zero-copy merge.
func ParseBatchHeader(data []byte) (baseOffset uint64, recordCount uint16, err error) {
	if len(data) < batchHeaderSize {
		return 0, 0, ErrCorruptBatch
	}
	baseOffset = binary.BigEndian.Uint64(data[0:8])
	recordCount = binary.BigEndian.Uint16(data[12:14])
	return baseOffset, recordCount, nil
}

// BatchSize returns the total encoded byte length of the batch at data[0:].
func BatchSize(data []byte) (int, error) {
	if len(data) < 12 {
		return 0, ErrCorruptBatch
	}
	batchLen := binary.BigEndian.Uint32(data[8:12])
	total := 12 + int64(batchLen)
	if total > int64(len(data)) {
		return 0, ErrCorruptBatch
	}
	return int(total), nil
}
