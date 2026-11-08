package hash

import (
	"bytes"
	"math"
	"testing"
)

func TestSum128IsDeterministic(t *testing.T) {
	t.Parallel()

	first := Sum128("example-key")
	second := Sum128("example-key")
	if first != second {
		t.Fatalf("Sum128() = %#v and %#v, want identical pairs", first, second)
	}
	if Sum64("example-key") != first.Primary {
		t.Fatal("Sum64() does not return the primary hash")
	}
	if Sum64WithSeed("example-key", 1) == Sum64WithSeed("example-key", 2) {
		t.Fatal("seed did not affect hash")
	}
}

func TestSupportedKeysUseDeterministicCanonicalEncodings(t *testing.T) {
	t.Parallel()

	if Sum128([]byte("key")) != Sum128([]byte("key")) {
		t.Fatal("byte hash is not deterministic")
	}
	if Sum128(int64(-1)) != Sum128(int64(-1)) {
		t.Fatal("numeric hash is not deterministic")
	}
}

func TestHashUsesCanonicalBytesAcrossSupportedRepresentations(t *testing.T) {
	if got := Sum64(""); got != 0xef46db3751d8e999 {
		t.Fatalf("empty xxhash64 = %016x, want known xxhash vector", got)
	}
	for _, pair := range []struct {
		name  string
		left  HashPair
		right HashPair
	}{
		{name: "string and bytes", left: Sum128("tenant-42"), right: Sum128([]byte("tenant-42"))},
		{name: "signed and unsigned integer", left: Sum128(int64(-1)), right: Sum128(uint64(math.MaxUint64))},
		{name: "byte and uint64", left: Sum128(byte(255)), right: Sum128(uint64(255))},
		{name: "uint and uint64", left: Sum128(uint(12)), right: Sum128(uint64(12))},
		{name: "int and int64", left: Sum128(int(-12)), right: Sum128(int64(-12))},
		{name: "int32 and int64", left: Sum128(int32(-12)), right: Sum128(int64(-12))},
		{name: "uint32 and uint64", left: Sum128(uint32(12)), right: Sum128(uint64(12))},
	} {
		t.Run(pair.name, func(t *testing.T) {
			if pair.left != pair.right {
				t.Fatalf("canonical hashes differ: %#v != %#v", pair.left, pair.right)
			}
		})
	}
}

func TestSeededHashKeepsEquivalentKeyEncodingsAndDoesNotMutateInput(t *testing.T) {
	input := []byte("customer-key")
	before := bytes.Clone(input)
	for _, seed := range []uint64{0, 1, math.MaxUint64} {
		if got, want := Sum64WithSeed(string(input), seed), Sum64WithSeed(input, seed); got != want {
			t.Errorf("seed %d hash string=%d bytes=%d", seed, got, want)
		}
	}
	for _, value := range []uint64{0, 1, math.MaxUint64} {
		if got, want := Sum64WithSeed(value, 17), Sum64WithSeed(int64(value), 17); got != want {
			t.Errorf("numeric encoding differs for %d: %d != %d", value, got, want)
		}
	}
	seed := uint64(91)
	want := Sum64WithSeed(uint64(42), seed)
	for _, got := range []uint64{
		Sum64WithSeed(byte(42), seed),
		Sum64WithSeed(uint(42), seed),
		Sum64WithSeed(int(42), seed),
		Sum64WithSeed(int32(42), seed),
		Sum64WithSeed(uint32(42), seed),
		Sum64WithSeed(int64(42), seed),
	} {
		if got != want {
			t.Errorf("numeric key encoding hash = %d, want %d", got, want)
		}
	}
	if !bytes.Equal(input, before) {
		t.Fatalf("hashing mutated the input: got %q, want %q", input, before)
	}
}
