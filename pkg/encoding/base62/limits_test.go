package base62

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckedCodecRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	if _, err := Encode(make([]byte, MaxInputBytes+1)); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("Encode oversized error = %v", err)
	}
	if _, err := Decode(strings.Repeat("0", MaxEncodedBytes+1)); !errors.Is(
		err,
		ErrInputTooLarge,
	) {
		t.Fatalf("Decode oversized error = %v", err)
	}
}
