package forge

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
)

const (
	batchLengthPrefixSize = 12 // BaseOffset(8) + BatchLength(4)
	batchHeaderSize       = 35 // BaseOffset(8)+BatchLength(4)+RecordCount(2)+Compression(1)+CRC(4)+Timestamp(8)+MaxTimestamp(8)

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
	BaseOffset   uint64
	RecordCount  uint16
	Compression  uint8
	Timestamp    int64
	MaxTimestamp int64
	Records      []Record
}

// EncodeBatch serializes a RecordBatch into dst (reused if large enough).
// Returns the encoded bytes.
func EncodeBatch(b *RecordBatch, dst []byte) ([]byte, error) {
	if b == nil || b.RecordCount == 0 || len(b.Records) == 0 {
		return nil, ErrEmptyBatch
	}
	if len(b.Records) > maxRecordsPerBatch || int(b.RecordCount) != len(b.Records) {
		return nil, ErrBatchTooLarge
	}
	if b.Compression != CompressionNone && b.Compression != CompressionLZ4 {
		return nil, ErrCorruptBatch
	}

	// Encode all records into a pooled buffer.
	bufPtr := recBufPool.Get().(*[]byte)
	recBuf := (*bufPtr)[:0]
	totalHeaders := 0
	for i := range b.Records {
		totalHeaders += len(b.Records[i].Headers)
		if totalHeaders > maxHeadersPerBatch {
			*bufPtr = recBuf[:0]
			recBufPool.Put(bufPtr)
			return nil, ErrMessageTooLarge
		}
		recordSize, err := encodedRecordSize(b.Records[i])
		if err != nil || recordSize > maxEncodedBatchBytes-len(recBuf) {
			*bufPtr = recBuf[:0]
			recBufPool.Put(bufPtr)
			return nil, ErrMessageTooLarge
		}
		recBuf = appendRecord(recBuf, &b.Records[i])
		if len(recBuf) > maxEncodedBatchBytes {
			*bufPtr = recBuf[:0]
			recBufPool.Put(bufPtr)
			return nil, ErrMessageTooLarge
		}
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
	if needed > maxEncodedBatchBytes {
		if actualCompression == CompressionNone {
			*bufPtr = recBuf[:0]
			recBufPool.Put(bufPtr)
		}
		return nil, ErrBatchTooLarge
	}

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

	// CRC32C covers the full batch except the CRC field itself, including the
	// assigned base offset and structural header fields.
	crc := crc32.Update(0, crc32cTable, dst[:crcPos])
	crc = crc32.Update(crc, crc32cTable, dst[crcPos+4:needed])
	binary.BigEndian.PutUint32(dst[crcPos:], crc)

	return dst, nil
}

// DecodeBatch deserializes one exact RecordBatch and returns owned record bytes.
func DecodeBatch(data []byte) (*RecordBatch, error) {
	return decodeBatch(data, true)
}

func decodeBatchBorrowed(data []byte) (*RecordBatch, error) {
	return decodeBatch(data, false)
}

type inspectedBatch struct {
	baseOffset   uint64
	recordCount  uint16
	compression  uint8
	timestamp    int64
	maxTimestamp int64
	recordData   []byte
}

func decodeBatch(data []byte, ownRecordBytes bool) (*RecordBatch, error) {
	inspected, err := inspectBatch(data)
	if err != nil {
		return nil, err
	}
	return decodeInspectedBatch(inspected, ownRecordBytes)
}

// inspectBatch validates the exact encoded extent, structural header, and
// checksum without decompressing or allocating record structures. Sparse
// reads use it while scanning batches before the requested offset so a highly
// compressible history cannot turn a small index scan into large transient
// allocations.
func inspectBatch(data []byte) (inspectedBatch, error) {
	if len(data) < batchHeaderSize || len(data) > maxEncodedBatchBytes {
		return inspectedBatch{}, ErrCorruptBatch
	}

	off := 0
	result := inspectedBatch{}
	result.baseOffset = binary.BigEndian.Uint64(data[off:])
	off += 8
	batchLen := binary.BigEndian.Uint32(data[off:])
	off += 4
	result.recordCount = binary.BigEndian.Uint16(data[off:])
	off += 2
	result.compression = data[off]
	off++
	if result.recordCount == 0 ||
		(result.compression != CompressionNone && result.compression != CompressionLZ4) {
		return inspectedBatch{}, ErrCorruptBatch
	}

	crcPosition := off
	storedCRC := binary.BigEndian.Uint32(data[off:])
	off += 4

	totalLen := batchLengthPrefixSize + int(batchLen)
	if totalLen < batchHeaderSize || totalLen > maxEncodedBatchBytes || len(data) != totalLen {
		return inspectedBatch{}, ErrCorruptBatch
	}

	// Verify CRC32C over the full batch except the stored CRC field.
	computed := crc32.Update(0, crc32cTable, data[:crcPosition])
	computed = crc32.Update(computed, crc32cTable, data[off:totalLen])
	if computed != storedCRC {
		return inspectedBatch{}, ErrChecksumMismatch
	}

	result.timestamp = int64(binary.BigEndian.Uint64(data[off:]))
	off += 8
	result.maxTimestamp = int64(binary.BigEndian.Uint64(data[off:]))
	off += 8
	result.recordData = data[off:totalLen]
	return result, nil
}

func decodeInspectedBatch(inspected inspectedBatch, ownRecordBytes bool) (*RecordBatch, error) {
	b := &RecordBatch{
		BaseOffset:   inspected.baseOffset,
		RecordCount:  inspected.recordCount,
		Compression:  inspected.compression,
		Timestamp:    inspected.timestamp,
		MaxTimestamp: inspected.maxTimestamp,
	}
	recData := inspected.recordData

	// Decompress if needed.
	if b.Compression != CompressionNone {
		comp := compressorFor(b.Compression)
		decompressed, err := comp.Decompress(nil, recData)
		if err != nil {
			return nil, errors.Join(ErrCorruptBatch, err)
		}
		recData = decompressed
	}

	b.Records = make([]Record, 0, b.RecordCount)
	remainingHeaderBudget := maxHeadersPerBatch
	for i := 0; i < int(b.RecordCount); i++ {
		rec, n, headerCount, err := decodeRecord(recData, remainingHeaderBudget)
		if err != nil {
			return nil, err
		}
		if rec.OffsetDelta != int64(i) {
			return nil, ErrCorruptRecord
		}
		remainingHeaderBudget -= headerCount
		if ownRecordBytes {
			rec = copyRecord(rec)
		}
		b.Records = append(b.Records, rec)
		recData = recData[n:]
	}
	if len(recData) != 0 {
		return nil, ErrCorruptBatch
	}

	return b, nil
}

// BatchSize returns the total encoded byte length of the batch at data[0:].
func BatchSize(data []byte) (int, error) {
	total, err := declaredBatchSize(data)
	if err != nil {
		return 0, err
	}
	if total > len(data) {
		return 0, ErrIncompleteBatch
	}
	return total, nil
}

func declaredBatchSize(data []byte) (int, error) {
	if len(data) < batchLengthPrefixSize {
		return 0, ErrIncompleteBatch
	}
	batchLen := binary.BigEndian.Uint32(data[8:batchLengthPrefixSize])
	total := int64(batchLengthPrefixSize) + int64(batchLen)
	if total < batchHeaderSize || total > maxEncodedBatchBytes {
		return 0, ErrCorruptBatch
	}
	return int(total), nil
}
