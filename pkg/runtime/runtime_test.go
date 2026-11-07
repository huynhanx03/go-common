package runtime

import (
	"bytes"
	"testing"
)

func TestRuntimeHashWrappersUseSameProcessContent(t *testing.T) {
	input := []byte("hash input")
	original := bytes.Clone(input)
	fromBytes := MemHash(input)
	if got := MemHashString(string(input)); got != fromBytes {
		t.Fatalf("byte and string hashes differ for the same content: %d != %d", fromBytes, got)
	}
	if !bytes.Equal(input, original) {
		t.Fatal("hashing changed the input bytes")
	}
	if MemHash(nil) != MemHashString("") {
		t.Fatal("nil and empty inputs produced different hashes")
	}
}

func TestRuntimeRandomBoundAndMonotonicClock(t *testing.T) {
	for _, upper := range []uint32{1, 2, 17, ^uint32(0)} {
		if got := Uint32n(upper); got >= upper {
			t.Fatalf("Uint32n(%d) = %d, outside [0,%d)", upper, got, upper)
		}
	}
	first := NanoTime()
	second := NanoTime()
	if second < first {
		t.Fatalf("monotonic clock moved backwards: %d then %d", first, second)
	}
}
