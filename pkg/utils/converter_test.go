package utils

import (
	"errors"
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
