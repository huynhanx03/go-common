package forge

import "encoding/binary"

// appendRecord encodes a single Record and appends to dst.
func appendRecord(dst []byte, r *Record) []byte {
	bodySize := varIntSize(r.TimestampDelta) +
		varIntSize(r.OffsetDelta) +
		varintBytesSize(r.Key) +
		varintBytesSize(r.Value) +
		varIntSize(int64(len(r.Headers)))

	for i := range r.Headers {
		bodySize += varintBytesSize(r.Headers[i].Key) +
			varintBytesSize(r.Headers[i].Value)
	}

	// Length prefix.
	dst = appendVarint(dst, int64(bodySize))

	// Body fields.
	dst = appendVarint(dst, r.TimestampDelta)
	dst = appendVarint(dst, r.OffsetDelta)
	dst = appendVarintBytes(dst, r.Key)
	dst = appendVarintBytes(dst, r.Value)
	dst = appendVarint(dst, int64(len(r.Headers)))
	for i := range r.Headers {
		dst = appendVarintBytes(dst, r.Headers[i].Key)
		dst = appendVarintBytes(dst, r.Headers[i].Value)
	}
	return dst
}

// decodeRecord decodes a single Record from data, returning bytes consumed.
func decodeRecord(data []byte) (Record, int, error) {
	if len(data) == 0 {
		return Record{}, 0, ErrCorruptRecord
	}

	recLen, n := binary.Varint(data)
	if n <= 0 || recLen < 0 {
		return Record{}, 0, ErrCorruptRecord
	}

	totalConsumed := n + int(recLen)
	if totalConsumed > len(data) {
		return Record{}, 0, ErrCorruptRecord
	}

	buf := data[n : n+int(recLen)]
	off := 0
	var r Record
	var ok bool

	r.TimestampDelta, off, ok = readVarint(buf, off)
	if !ok {
		return Record{}, 0, ErrCorruptRecord
	}
	r.OffsetDelta, off, ok = readVarint(buf, off)
	if !ok {
		return Record{}, 0, ErrCorruptRecord
	}
	r.Key, off, ok = readVarintBytes(buf, off)
	if !ok {
		return Record{}, 0, ErrCorruptRecord
	}
	r.Value, off, ok = readVarintBytes(buf, off)
	if !ok {
		return Record{}, 0, ErrCorruptRecord
	}

	headerCount, newOff, hOk := readVarint(buf, off)
	if !hOk || headerCount < 0 || headerCount > maxHeadersPerRecord {
		return Record{}, 0, ErrCorruptRecord
	}
	off = newOff

	if headerCount > 0 {
		r.Headers = make([]Header, headerCount)
		for i := range r.Headers {
			r.Headers[i].Key, off, ok = readVarintBytes(buf, off)
			if !ok {
				return Record{}, 0, ErrCorruptRecord
			}
			r.Headers[i].Value, off, ok = readVarintBytes(buf, off)
			if !ok {
				return Record{}, 0, ErrCorruptRecord
			}
		}
	}

	return r, totalConsumed, nil
}

// --- varint helpers ---

func appendVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func appendVarintBytes(dst []byte, b []byte) []byte {
	if b == nil {
		return appendVarint(dst, -1)
	}
	dst = appendVarint(dst, int64(len(b)))
	return append(dst, b...)
}

func readVarint(data []byte, off int) (int64, int, bool) {
	if off >= len(data) {
		return 0, off, false
	}
	v, n := binary.Varint(data[off:])
	if n <= 0 {
		return 0, off, false
	}
	return v, off + n, true
}

func readVarintBytes(data []byte, off int) ([]byte, int, bool) {
	length, newOff, ok := readVarint(data, off)
	if !ok {
		return nil, off, false
	}
	if length < 0 {
		return nil, newOff, true // null
	}
	end := newOff + int(length)
	if end > len(data) {
		return nil, off, false
	}
	return data[newOff:end], end, true
}

func varIntSize(v int64) int {
	ux := uint64(v) << 1
	if v < 0 {
		ux = ^ux
	}
	size := 1
	for ux >= 0x80 {
		ux >>= 7
		size++
	}
	return size
}

func varintBytesSize(b []byte) int {
	if b == nil {
		return varIntSize(-1)
	}
	return varIntSize(int64(len(b))) + len(b)
}
