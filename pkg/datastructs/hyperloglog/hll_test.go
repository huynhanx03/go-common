package hyperloglog

import (
	"math"
	"math/rand"
	"testing"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

func TestEstimateHandlesDuplicatesAndUniformStreams(t *testing.T) {
	if got := New().Count(); got != 0 {
		t.Fatalf("empty estimate = %d, want 0", got)
	}

	first := New()
	rng := rand.New(rand.NewSource(42))
	values := make([]uint64, 50_000)
	for i := range values {
		values[i] = rng.Uint64()
		first.Add(values[i])
	}
	got := first.Count()
	if math.Abs(float64(got-int64(len(values))))/float64(len(values)) > 0.05 {
		t.Fatalf("estimate = %d for %d distinct inputs, outside 5%% tolerance", got, len(values))
	}
	for _, value := range values {
		first.Add(value)
	}
	if duplicateEstimate := first.Count(); duplicateEstimate != got {
		t.Fatalf("repeated inputs changed estimate from %d to %d", got, duplicateEstimate)
	}
}

func TestMergeCloneAndPersistenceKeepSketchBehavior(t *testing.T) {
	left, right, union := New(), New(), New()
	for value := uint64(0); value < 30_000; value++ {
		left.Add(value * 3)
		right.Add(value*3 + 1)
		union.Add(value * 3)
		union.Add(value*3 + 1)
	}
	left.Merge(right)
	if got, want := left.Count(), union.Count(); got != want {
		t.Fatalf("merged estimate = %d, union estimate = %d", got, want)
	}

	clone := New()
	clone.Add(0)
	independent := clone.Clone()
	independent.Add(1)
	if clone.Count() != 1 || independent.Count() != 2 {
		t.Fatalf("clone updates were shared: original=%d clone=%d", clone.Count(), independent.Count())
	}

	encoded, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	var restored HLL
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Count() != left.Count() {
		t.Fatalf("restored estimate = %d, want %d", restored.Count(), left.Count())
	}
}

func TestRestoreRejectsInvalidRegistersWithoutChangingState(t *testing.T) {
	valid := New()
	valid.Add(17)
	want := valid.Count()
	base, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	invalidRegisters := make([]uint8, m)
	invalidRegisters[0] = 64
	invalid, err := json.Marshal(invalidRegisters)
	if err != nil {
		t.Fatal(err)
	}
	wrongLength, err := json.Marshal(make([]uint8, m-1))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("null"), wrongLength, invalid, []byte("{")} {
		var restored HLL
		if err := json.Unmarshal(base, &restored); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &restored); err == nil {
			t.Errorf("accepted invalid sketch state %q", data[:min(len(data), 20)])
		}
		if restored.Count() != want {
			t.Errorf("failed restore changed usable estimate from %d to %d", want, restored.Count())
		}
	}
}

func TestZeroHashUsesMaximumValidRegisterRank(t *testing.T) {
	sketch := New()
	maxRank := uint8(64 - p + 1)
	if !sketch.Add(0) || sketch.registers[0] != maxRank {
		t.Fatalf("zero hash register rank = %d, want %d", sketch.registers[0], maxRank)
	}
	if got := sketch.Count(); got != 1 {
		t.Fatalf("zero hash produced invalid estimate %d", got)
	}
}
