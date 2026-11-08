package morris

import (
	"math/rand"
	"testing"
)

func TestCounterStartsIncrementsAndResets(t *testing.T) {
	counter := New()
	if counter.Count() != 0 || !counter.Increment() || counter.Count() != 1 {
		t.Fatal("first update did not move the counter from zero to one")
	}
	counter.Reset()
	if counter.Count() != 0 || counter.RawValue() != 0 {
		t.Fatal("reset retained the previous estimate")
	}
}

func TestSeededCountersTrackAggregateUpdates(t *testing.T) {
	const trials, increments = 256, 2_000
	var total uint64
	for seed := int64(1); seed <= trials; seed++ {
		counter := New()
		counter.rnd = rand.New(rand.NewSource(seed))
		for range increments {
			counter.Increment()
		}
		total += counter.Count()
	}
	mean := float64(total) / trials
	relativeError := (mean - increments) / increments
	if relativeError < -0.12 || relativeError > 0.12 {
		t.Fatalf("mean estimate %.1f for %d updates is outside the 12%% envelope", mean, increments)
	}
}

func TestCounterSaturatesAtUint64Range(t *testing.T) {
	counter := New()
	counter.SetRawValue(64)
	if counter.Increment() || counter.Count() != ^uint64(0) {
		t.Fatalf("counter did not saturate at uint64 maximum: value=%d count=%d", counter.RawValue(), counter.Count())
	}
	counter.SetRawValue(255)
	if counter.Increment() || counter.Count() != ^uint64(0) {
		t.Fatalf("out-of-range restored value wrapped: value=%d count=%d", counter.RawValue(), counter.Count())
	}
}
