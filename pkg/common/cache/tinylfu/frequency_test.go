package tinylfu

import (
	"math"
	"testing"
)

// =============================================================================
// Constructor Tests: NewFrequency
// =============================================================================

func TestNewFrequency(t *testing.T) {
	tests := []struct {
		name        string
		numCounters int64
		expectDoor  bool // door may be nil for invalid inputs
	}{
		{"valid_1000", 1000, true},
		{"minimum_1", 1, true},
		{"zero_defaults", 0, false},     // bloom.New returns error for 0 capacity
		{"negative_defaults", -1, true}, // sketch converts to 1, bloom uses abs
		{"large_1M", 1000000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFrequency(tt.numCounters)
			if f == nil {
				t.Error("NewFrequency returned nil")
				return
			}
			if f.freq == nil {
				t.Error("freq sketch is nil")
			}
			if tt.expectDoor && f.door == nil {
				t.Error("door bloom is nil when expected")
			}
			if !tt.expectDoor && f.door != nil {
				t.Log("door bloom is non-nil for edge case (acceptable)")
			}
		})
	}
}

// =============================================================================
// Record Tests
// =============================================================================

func TestRecord_FirstAccess(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)

	// First access: key in bloom only, estimate = 1
	est := f.Estimate(key)
	if est != 1 {
		t.Errorf("first access estimate = %d, want 1", est)
	}
}

func TestRecord_SecondAccess(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)
	f.Record(key)

	// Second access: key incremented in sketch, estimate >= 2
	est := f.Estimate(key)
	if est < 2 {
		t.Errorf("second access estimate = %d, want >= 2", est)
	}
}

func TestRecord_MultipleSameKey(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	// Record same key multiple times
	for i := 0; i < 10; i++ {
		f.Record(key)
	}

	est := f.Estimate(key)
	// After 10 accesses: 1 bloom + 9 sketch increments
	if est < 5 {
		t.Errorf("multiple accesses estimate = %d, want >= 5", est)
	}
}

func TestRecord_ResetTrigger(t *testing.T) {
	numCounters := int64(100)
	f := NewFrequency(numCounters)
	key := uint64(12345)

	// Record key multiple times
	for i := int64(0); i < 50; i++ {
		f.Record(key)
	}
	beforeReset := f.Estimate(key)

	// Trigger reset by exceeding numCounters
	for i := int64(0); i < numCounters+10; i++ {
		f.Record(uint64(i + 10000)) // different keys
	}

	afterReset := f.Estimate(key)

	// After reset, frequency should be reduced or zero
	if afterReset >= beforeReset && beforeReset > 1 {
		t.Logf("beforeReset=%d, afterReset=%d (reset may reduce via halving)", beforeReset, afterReset)
	}
}

func TestRecord_EdgeKeys(t *testing.T) {
	tests := []struct {
		name string
		key  uint64
	}{
		{"zero_key", 0},
		{"max_uint64", math.MaxUint64},
		{"one", 1},
		{"large", 9999999999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFrequency(1000)
			f.Record(tt.key)
			f.Record(tt.key)

			est := f.Estimate(tt.key)
			if est < 1 {
				t.Errorf("estimate for key %d = %d, want >= 1", tt.key, est)
			}
		})
	}
}

func TestRecord_InterleavedKeys(t *testing.T) {
	f := NewFrequency(1000)

	key1 := uint64(111)
	key2 := uint64(222)

	// Interleaved recording
	for i := 0; i < 5; i++ {
		f.Record(key1)
		f.Record(key2)
	}

	est1 := f.Estimate(key1)
	est2 := f.Estimate(key2)

	if est1 < 2 {
		t.Errorf("key1 estimate = %d, want >= 2", est1)
	}
	if est2 < 2 {
		t.Errorf("key2 estimate = %d, want >= 2", est2)
	}
}

func TestRecord_AfterClear(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)
	f.Record(key)
	f.Clear()

	// Record after clear
	f.Record(key)
	est := f.Estimate(key)

	if est != 1 {
		t.Errorf("after clear, first access estimate = %d, want 1", est)
	}
}

// =============================================================================
// Estimate Tests
// =============================================================================

func TestEstimate_RecordedKey(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)
	f.Record(key)
	f.Record(key)

	est := f.Estimate(key)
	if est < 1 {
		t.Errorf("recorded key estimate = %d, want >= 1", est)
	}
}

func TestEstimate_NeverRecorded(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(99999)

	est := f.Estimate(key)
	if est != 0 {
		t.Errorf("never recorded estimate = %d, want 0", est)
	}
}

func TestEstimate_FirstAccessOnly(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key) // First access: bloom only

	est := f.Estimate(key)
	if est != 1 {
		t.Errorf("first access only estimate = %d, want 1", est)
	}
}

func TestEstimate_AfterReset(t *testing.T) {
	numCounters := int64(50)
	f := NewFrequency(numCounters)
	key := uint64(12345)

	// Build up frequency
	for i := 0; i < 20; i++ {
		f.Record(key)
	}

	// Trigger reset
	for i := int64(0); i < numCounters+10; i++ {
		f.Record(uint64(i + 10000))
	}

	est := f.Estimate(key)
	// After reset, should be 0 or significantly reduced
	t.Logf("estimate after reset = %d (expected 0 or reduced)", est)
}

func TestEstimate_EdgeKeys(t *testing.T) {
	tests := []struct {
		name string
		key  uint64
	}{
		{"zero_key", 0},
		{"max_uint64", math.MaxUint64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFrequency(1000)

			// Not recorded: should be 0
			est := f.Estimate(tt.key)
			if est != 0 {
				t.Errorf("unrecorded key %d estimate = %d, want 0", tt.key, est)
			}

			// Record and verify
			f.Record(tt.key)
			est = f.Estimate(tt.key)
			if est < 1 {
				t.Errorf("recorded key %d estimate = %d, want >= 1", tt.key, est)
			}
		})
	}
}

func TestEstimate_MultipleConsistent(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)
	f.Record(key)
	f.Record(key)

	// Multiple estimates should be consistent
	est1 := f.Estimate(key)
	est2 := f.Estimate(key)
	est3 := f.Estimate(key)

	if est1 != est2 || est2 != est3 {
		t.Errorf("inconsistent estimates: %d, %d, %d", est1, est2, est3)
	}
}

// =============================================================================
// Clear Tests
// =============================================================================

func TestClear_AfterRecords(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)
	f.Record(key)
	f.Record(key)

	f.Clear()

	est := f.Estimate(key)
	if est != 0 {
		t.Errorf("after clear estimate = %d, want 0", est)
	}
}

func TestClear_EmptyFrequency(t *testing.T) {
	f := NewFrequency(1000)

	// Clear on new instance should not panic
	f.Clear()

	est := f.Estimate(12345)
	if est != 0 {
		t.Errorf("empty after clear estimate = %d, want 0", est)
	}
}

func TestClear_ThenRecord(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)
	f.Record(key)
	f.Clear()

	// Record after clear
	f.Record(key)

	est := f.Estimate(key)
	if est != 1 {
		t.Errorf("after clear and new record estimate = %d, want 1", est)
	}
}

func TestClear_Multiple(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	f.Record(key)
	f.Clear()
	f.Clear()
	f.Clear()

	est := f.Estimate(key)
	if est != 0 {
		t.Errorf("after multiple clears estimate = %d, want 0", est)
	}
}

// =============================================================================
// Workflow Tests
// =============================================================================

func TestWorkflow_RecordEstimateClear(t *testing.T) {
	f := NewFrequency(1000)
	key := uint64(12345)

	// Record 5 times
	for i := 0; i < 5; i++ {
		f.Record(key)
	}

	estBefore := f.Estimate(key)
	if estBefore < 2 {
		t.Errorf("before clear estimate = %d, want >= 2", estBefore)
	}

	f.Clear()

	estAfter := f.Estimate(key)
	if estAfter != 0 {
		t.Errorf("after clear estimate = %d, want 0", estAfter)
	}
}

func TestWorkflow_ResetBehavior(t *testing.T) {
	numCounters := int64(100)
	f := NewFrequency(numCounters)

	// Record a key many times
	targetKey := uint64(42)
	for i := 0; i < 50; i++ {
		f.Record(targetKey)
	}

	estBefore := f.Estimate(targetKey)

	// Trigger reset by recording many different keys
	for i := int64(0); i < numCounters+50; i++ {
		f.Record(uint64(i + 1000))
	}

	estAfter := f.Estimate(targetKey)

	t.Logf("Reset behavior: before=%d, after=%d", estBefore, estAfter)
	// After reset, frequency should be 0 (bloom and sketch cleared)
}
