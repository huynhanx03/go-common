package utils

import (
	"math"
	"testing"
)

func TestIsPowerOfTwo(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		input int
		want  bool
	}{
		{1, true},
		{2, true},
		{4, true},
		{1024, true},
		{1 << 62, true},
		{0, false},
		{3, false},
		{6, false},
		{100, false},
		{math.MaxInt, false},
		{-1, false},
		{-2, false},
		{-8, false},
		{math.MinInt, false},
	} {
		if got := IsPowerOfTwo(testCase.input); got != testCase.want {
			t.Errorf("IsPowerOfTwo(%d) = %v, want %v", testCase.input, got, testCase.want)
		}
	}
}

func TestCeilToPowerOfTwoRoundsUp(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		input int
		want  int
	}{
		{3, 4},
		{4, 4},
		{5, 8},
		{7, 8},
		{8, 8},
		{100, 128},
		{1 << 31, 1 << 31},
		{1<<31 + 1, 1 << 32},
		{1 << 40, 1 << 40},
		{1<<40 - 1, 1 << 40},
		{1 << 62, 1 << 62},
	} {
		if got := CeilToPowerOfTwo(testCase.input); got != testCase.want {
			t.Errorf("CeilToPowerOfTwo(%d) = %d, want %d", testCase.input, got, testCase.want)
		}
	}
}

// CeilToPowerOfTwo clamps everything at or below 2 to 2, so it never returns 1
// even though 1 is itself a power of two. Callers sizing a buffer or a shard
// table rely on this floor of 2.
func TestCeilToPowerOfTwoClampsSmallInputsToTwo(t *testing.T) {
	t.Parallel()

	for _, input := range []int{math.MinInt, -8, -1, 0, 1, 2} {
		if got := CeilToPowerOfTwo(input); got != 2 {
			t.Errorf("CeilToPowerOfTwo(%d) = %d, want 2", input, got)
		}
	}
}

func TestCeilToPowerOfTwoPanicsAboveHeadBit(t *testing.T) {
	t.Parallel()

	assertPanics := func(t *testing.T, input int, want bool) {
		t.Helper()
		panicked := func() (panicked bool) {
			defer func() {
				panicked = recover() != nil
			}()
			CeilToPowerOfTwo(input)
			return false
		}()
		if panicked != want {
			t.Errorf("CeilToPowerOfTwo(%d) panicked = %v, want %v", input, panicked, want)
		}
	}

	assertPanics(t, maxIntHeadBit-1, false)
	assertPanics(t, maxIntHeadBit, false)
	assertPanics(t, maxIntHeadBit+1, true)
	assertPanics(t, math.MaxInt, true)
}

// FloorToPowerOfTwo must return the largest power of two that is <= n. The
// doc comment says "next-highest", but the implementation rounds down and the
// name says down; this pins the rounding direction.
func TestFloorToPowerOfTwoRoundsDown(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		input int
		want  int
	}{
		{3, 2},
		{4, 4},
		{5, 4},
		{7, 4},
		{8, 8},
		{100, 64},
		{1<<31 - 1, 1 << 30},
		{1 << 31, 1 << 31},
		{1<<32 - 1, 1 << 31},
		{1 << 32, 1 << 32},
		{1<<32 + 1, 1 << 32},
		{1 << 33, 1 << 33},
		{1<<40 + 12345, 1 << 40},
		{1 << 62, 1 << 62},
		{math.MaxInt, 1 << 62},
	} {
		if got := FloorToPowerOfTwo(testCase.input); got != testCase.want {
			t.Errorf("FloorToPowerOfTwo(%d) = %d, want %d", testCase.input, got, testCase.want)
		}
	}
}

// The defining property: for every positive n the result is a power of two,
// does not exceed n, and doubling it does exceed n.
func TestFloorToPowerOfTwoSatisfiesFloorProperty(t *testing.T) {
	t.Parallel()

	for exponent := 1; exponent <= 62; exponent++ {
		for _, input := range []int{1<<exponent - 1, 1 << exponent, 1<<exponent + 1} {
			if input < 3 {
				continue
			}
			got := FloorToPowerOfTwo(input)
			if !IsPowerOfTwo(got) {
				t.Errorf("FloorToPowerOfTwo(%d) = %d, which is not a power of two", input, got)
				continue
			}
			if got > input {
				t.Errorf("FloorToPowerOfTwo(%d) = %d, which exceeds the input", input, got)
			}
			if got <= math.MaxInt/2 && got*2 <= input {
				t.Errorf("FloorToPowerOfTwo(%d) = %d, but %d also fits", input, got, got*2)
			}
		}
	}
}

// Inputs at or below 2 are returned unchanged, including zero and negatives,
// so callers must guard the result themselves.
func TestFloorToPowerOfTwoReturnsSmallInputsUnchanged(t *testing.T) {
	t.Parallel()

	for _, input := range []int{math.MinInt, -8, -1, 0, 1, 2} {
		if got := FloorToPowerOfTwo(input); got != input {
			t.Errorf("FloorToPowerOfTwo(%d) = %d, want %d", input, got, input)
		}
	}
}

// Exact midpoints resolve upward, because the comparison is a strict "<".
func TestClosestPowerOfTwo(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		input int
		want  int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{5, 4},
		{6, 8},
		{7, 8},
		{96, 128},
		{100, 128},
		{1<<20 + 1, 1 << 20},
		{1<<21 - 1, 1 << 21},
	} {
		if got := ClosestPowerOfTwo(testCase.input); got != testCase.want {
			t.Errorf("ClosestPowerOfTwo(%d) = %d, want %d", testCase.input, got, testCase.want)
		}
	}
}

func TestSpread32PlacesBitsOnEvenPositions(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		input uint32
		want  uint64
	}{
		{0, 0},
		{1, 0x1},
		{2, 0x4},
		{3, 0x5},
		{0xFF, 0x5555},
		{0xFFFFFFFF, 0x5555555555555555},
	} {
		if got := Spread32(testCase.input); got != testCase.want {
			t.Errorf("Spread32(%#x) = %#x, want %#x", testCase.input, got, testCase.want)
		}
	}
}

func TestSpread32NeverSetsOddBits(t *testing.T) {
	t.Parallel()

	const oddBits = 0xAAAAAAAAAAAAAAAA
	for _, input := range []uint32{0, 1, 2, 0xDEADBEEF, 0x80000000, 0xFFFFFFFF} {
		if got := Spread32(input) & oddBits; got != 0 {
			t.Errorf("Spread32(%#x) set odd bits %#x", input, got)
		}
	}
}

func TestSquash64InvertsSpread32(t *testing.T) {
	t.Parallel()

	for _, input := range []uint32{
		0, 1, 2, 3, 0xFF, 0xDEADBEEF, 0x12345678, 0x80000000, 0xFFFFFFFF,
	} {
		if got := Squash64(Spread32(input)); got != input {
			t.Errorf("Squash64(Spread32(%#x)) = %#x, want %#x", input, got, input)
		}
	}
}

// Squash64 masks the odd positions away, so interleaved data in the odd bits
// (the other coordinate of a Morton code) cannot corrupt the result.
func TestSquash64IgnoresOddBits(t *testing.T) {
	t.Parallel()

	const oddBits = 0xAAAAAAAAAAAAAAAA
	for _, input := range []uint32{0, 1, 0xDEADBEEF, 0xFFFFFFFF} {
		spread := Spread32(input)
		if got := Squash64(spread | oddBits); got != input {
			t.Errorf("Squash64(%#x | oddBits) = %#x, want %#x", spread, got, input)
		}
	}
}
