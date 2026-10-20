package hash

import "testing"

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
