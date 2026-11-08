package encoding

import (
	"errors"
	"math"
	"testing"
)

func TestBase62IntegerCodecMatchesKnownVectors(t *testing.T) {
	for _, test := range []struct {
		value int64
		text  string
	}{
		{0, "0"}, {61, "z"}, {62, "10"}, {math.MaxInt64, "AzL8n0Y58m7"},
	} {
		encoded, err := Base62EncodeChecked(test.value)
		if err != nil || encoded != test.text {
			t.Fatalf("encode %d = %q, %v; want %q", test.value, encoded, err, test.text)
		}
		decoded, err := Base62Decode(test.text)
		if err != nil || decoded != test.value {
			t.Fatalf("decode %q = %d, %v; want %d", test.text, decoded, err, test.value)
		}
	}
	if got := Base62Encode(math.MinInt64); got != "AzL8n0Y58m8" {
		t.Fatalf("absolute MinInt64 encoding = %q", got)
	}
}

func TestBase62IntegerCodecRejectsInvalidAndOverflowingInput(t *testing.T) {
	if _, err := Base62EncodeChecked(-1); !errors.Is(err, ErrNegativeBase62Integer) {
		t.Fatalf("negative checked encode error = %v", err)
	}
	for _, text := range []string{"", "!", "é", "000000000000"} {
		if _, err := Base62Decode(text); !errors.Is(err, ErrInvalidBase62Integer) {
			t.Errorf("decode %q error = %v, want invalid encoding", text, err)
		}
	}
	if _, err := Base62Decode("AzL8n0Y58m8"); !errors.Is(err, ErrBase62IntegerOverflow) {
		t.Fatalf("decode MinInt64 absolute value error = %v", err)
	}
}

func FuzzBase62NonNegativeRoundTrip(f *testing.F) {
	for _, seed := range []int64{0, 1, 61, 62, math.MaxInt64} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value int64) {
		if value < 0 {
			t.Skip()
		}
		encoded, err := Base62EncodeChecked(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Base62Decode(encoded)
		if err != nil || decoded != value {
			t.Fatalf("round trip %d -> %q -> %d, %v", value, encoded, decoded, err)
		}
	})
}
