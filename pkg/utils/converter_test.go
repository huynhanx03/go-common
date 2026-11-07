package utils

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestStableStringByteConversionsCopyMemory(t *testing.T) {
	t.Parallel()

	source := string([]byte("immutable"))
	converted := StringToBytes(source)
	converted[0] = 'X'
	if source != "immutable" {
		t.Fatalf("StringToBytes aliased source: %q", source)
	}

	bytes := []byte("owned")
	text := BytesToString(bytes)
	bytes[0] = 'X'
	if text != "owned" {
		t.Fatalf("BytesToString aliased source: %q", text)
	}
}

func TestCheckedIntegerConversionsRejectInvalidLengths(t *testing.T) {
	t.Parallel()

	if _, err := BytesToUint64([]byte{1}); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("BytesToUint64 short error = %v", err)
	}
	if _, err := BytesToUint32(make([]byte, 5)); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("BytesToUint32 long error = %v", err)
	}
	if _, err := BytesToUint16ByBigEndian(nil); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("BytesToUint16ByBigEndian error = %v", err)
	}
	if _, err := BytesToUint64Slice(make([]byte, 9)); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("BytesToUint64Slice error = %v", err)
	}
}

func TestUint64SliceConversionCopies(t *testing.T) {
	t.Parallel()

	data := Uint64ToBytes(42)
	values, err := BytesToUint64Slice(data)
	if err != nil || len(values) != 1 || values[0] != 42 {
		t.Fatalf("values=%v error=%v", values, err)
	}
	values[0] = 7
	decoded, err := BytesToUint64(data)
	if err != nil || decoded != 42 {
		t.Fatalf("conversion retained unsafe alias: %d, %v", decoded, err)
	}
}

func TestIntegerConvertersMatchWireVectors(t *testing.T) {
	checks := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"uint64 little endian", Uint64ToBytes(0x0102030405060708), []byte{8, 7, 6, 5, 4, 3, 2, 1}},
		{"uint64 big endian", Uint64ToBytesByBigEndian(0x0102030405060708), []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{"int64 negative", Int64ToBytes(-2), []byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"uint32 little endian", Uint32ToBytes(0x01020304), []byte{4, 3, 2, 1}},
		{"uint16 little endian", Uint16ToBytes(0x0102), []byte{2, 1}},
		{"uint16 big endian", Uint16ToBytesByBigEndian(0x0102), []byte{1, 2}},
	}
	for _, check := range checks {
		if !bytes.Equal(check.got, check.want) {
			t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}

	if got, err := BytesToUint64([]byte{8, 7, 6, 5, 4, 3, 2, 1}); err != nil || got != 0x0102030405060708 {
		t.Fatalf("little endian decode = %#x, %v", got, err)
	}
	if got, err := BytesToUint64ByBigEndian([]byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil || got != 0x0102030405060708 {
		t.Fatalf("big endian decode = %#x, %v", got, err)
	}
	if got, err := BytesToInt64(Int64ToBytes(math.MinInt64)); err != nil || got != math.MinInt64 {
		t.Fatalf("signed boundary decode = %d, %v", got, err)
	}
	if got, err := BytesToUint32([]byte{4, 3, 2, 1}); err != nil || got != 0x01020304 {
		t.Fatalf("uint32 decode = %#x, %v", got, err)
	}
	if got, err := BytesToUint16([]byte{2, 1}); err != nil || got != 0x0102 {
		t.Fatalf("uint16 little endian decode = %#x, %v", got, err)
	}
	if got, err := BytesToUint16ByBigEndian([]byte{1, 2}); err != nil || got != 0x0102 {
		t.Fatalf("uint16 big endian decode = %#x, %v", got, err)
	}
	if got, err := BytesToUint64Slice([]byte{8, 7, 6, 5, 4, 3, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0}); err != nil || len(got) != 2 || got[0] != 0x0102030405060708 || got[1] != 0 {
		t.Fatalf("uint64 slice decode = %v, %v", got, err)
	}
}

func TestIntegerConvertersRejectEveryWrongWidth(t *testing.T) {
	for _, check := range []struct {
		name string
		call func([]byte) error
	}{
		{"uint64 little endian", func(b []byte) error { _, err := BytesToUint64(b); return err }},
		{"uint64 big endian", func(b []byte) error { _, err := BytesToUint64ByBigEndian(b); return err }},
		{"int64", func(b []byte) error { _, err := BytesToInt64(b); return err }},
		{"uint32", func(b []byte) error { _, err := BytesToUint32(b); return err }},
		{"uint16 little endian", func(b []byte) error { _, err := BytesToUint16(b); return err }},
		{"uint16 big endian", func(b []byte) error { _, err := BytesToUint16ByBigEndian(b); return err }},
	} {
		for _, input := range [][]byte{nil, make([]byte, 1), make([]byte, 3), make([]byte, 5), make([]byte, 7), make([]byte, 9)} {
			if err := check.call(input); !errors.Is(err, ErrInvalidLength) {
				t.Errorf("%s length %d error = %v", check.name, len(input), err)
			}
		}
	}
	for _, input := range [][]byte{[]byte{1}, make([]byte, 9)} {
		if _, err := BytesToUint64Slice(input); !errors.Is(err, ErrInvalidLength) {
			t.Errorf("uint64 slice length %d error = %v", len(input), err)
		}
	}
}
