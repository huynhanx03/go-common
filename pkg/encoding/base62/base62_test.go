package base62

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/google/uuid"
)

func TestRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		{0},
		{0, 0, 0},
		{0, 0, 1, 2, 3}, // leading zeros must survive
		{255, 255, 255, 255},
		[]byte("hello world"),
	}
	for _, b := range cases {
		encoded, err := Encode(b)
		if err != nil {
			t.Fatalf("Encode(%v): %v", b, err)
		}
		got, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode(%v): %v", b, err)
		}
		if !bytes.Equal(got, b) {
			t.Fatalf("round trip %v → %q → %v", b, encoded, got)
		}
	}
}

func TestRoundTripRandom(t *testing.T) {
	for range 200 {
		b := make([]byte, 16)
		rand.Read(b)
		encoded, encodeErr := Encode(b)
		got, err := Decode(encoded)
		if encodeErr != nil {
			err = encodeErr
		}
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("round trip failed for %v: %v", b, err)
		}
	}
}

func TestUUIDRoundTrip(t *testing.T) {
	id := uuid.Must(uuid.NewV7())

	s, err := Encode(id[:])
	if err != nil {
		t.Fatal(err)
	}
	if len(s) > 22 {
		t.Fatalf("encoded UUID is %d chars, want ≤ 22 (got %q)", len(s), s)
	}

	b, err := Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := uuid.FromBytes(b)
	if err != nil || back != id {
		t.Fatalf("uuid round trip: %v → %q → %v (%v)", id, s, back, err)
	}
}

func TestDecodeInvalidCharacter(t *testing.T) {
	for _, s := range []string{"abc-def", "hé", "a b"} {
		if _, err := Decode(s); err == nil {
			t.Fatalf("Decode(%q) must fail", s)
		}
	}
}

func TestEncodeSortsLikeBytes(t *testing.T) {
	// Same-length inputs must encode to strings that sort identically —
	// this is why the alphabet is ASCII-ordered (digits < upper < lower).
	a, err := Encode([]byte{1, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode([]byte{2, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) == len(b) && a >= b {
		t.Fatalf("ordering broken: %q >= %q", a, b)
	}
}
