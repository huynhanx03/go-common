package utils

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestBackoffRejectsInvalidInputsAndSaturates(t *testing.T) {
	t.Parallel()

	if _, err := CalculateBackoff(-1, time.Second, time.Minute, nil); !errors.Is(
		err,
		ErrInvalidBackoff,
	) {
		t.Fatalf("negative attempt error = %v", err)
	}
	if _, err := CalculateBackoff(1, 0, time.Minute, nil); !errors.Is(
		err,
		ErrInvalidBackoff,
	) {
		t.Fatalf("zero base error = %v", err)
	}
	delay, err := CalculateBackoff(math.MaxInt, time.Second, time.Minute, nil)
	if err != nil || delay != time.Minute {
		t.Fatalf("saturated delay = %s, %v", delay, err)
	}
}
