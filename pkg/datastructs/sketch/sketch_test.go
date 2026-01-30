package sketch

import (
	"math"
	"testing"
)

// =============================================================================
// Constructor Tests: New()
// =============================================================================

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		numCounters int64
		wantNonNil  bool
	}{
		{"valid_standard", 1000, true},
		{"zero_defaults", 0, true},
		{"negative_defaults", -1, true},
		{"minimum_valid", 1, true},
		{"large", 10_000_000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.numCounters)
			if (got != nil) != tt.wantNonNil {
				t.Errorf("New(%d) = %v, want non-nil: %v", tt.numCounters, got, tt.wantNonNil)
			}
		})
	}
}

// =============================================================================
// Increment Tests
// =============================================================================

func TestIncrement(t *testing.T) {
	t.Run("happy_increment_and_estimate", func(t *testing.T) {
		s := New(1000)
		s.Increment(12345)
		if got := s.Estimate(12345); got != 1 {
			t.Errorf("Estimate() = %d, want 1", got)
		}
	})

	t.Run("boundary_zero", func(t *testing.T) {
		s := New(1000)
		s.Increment(0)
		if got := s.Estimate(0); got != 1 {
			t.Errorf("Estimate(0) = %d, want 1", got)
		}
	})

	t.Run("boundary_max_uint64", func(t *testing.T) {
		s := New(1000)
		s.Increment(math.MaxUint64)
		if got := s.Estimate(math.MaxUint64); got != 1 {
			t.Errorf("Estimate(MaxUint64) = %d, want 1", got)
		}
	})

	t.Run("repeated_increments", func(t *testing.T) {
		s := New(1000)
		for i := 0; i < 5; i++ {
			s.Increment(999)
		}
		if got := s.Estimate(999); got != 5 {
			t.Errorf("Estimate() = %d, want 5", got)
		}
	})

	t.Run("saturation_at_15", func(t *testing.T) {
		s := New(1000)
		for i := 0; i < 20; i++ {
			s.Increment(888)
		}
		if got := s.Estimate(888); got != 15 {
			t.Errorf("Estimate() = %d, want 15 (max)", got)
		}
	})

	t.Run("increment_after_clear", func(t *testing.T) {
		s := New(1000)
		s.Increment(100)
		s.Clear()
		s.Increment(200)
		if got := s.Estimate(200); got != 1 {
			t.Errorf("Estimate(200) after Clear = %d, want 1", got)
		}
	})
}

// =============================================================================
// Estimate Tests
// =============================================================================

func TestEstimate(t *testing.T) {
	t.Run("happy_estimate", func(t *testing.T) {
		s := New(1000)
		s.Increment(42)
		s.Increment(42)
		s.Increment(42)
		if got := s.Estimate(42); got != 3 {
			t.Errorf("Estimate() = %d, want 3", got)
		}
	})

	t.Run("estimate_empty", func(t *testing.T) {
		s := New(1000)
		if got := s.Estimate(12345); got != 0 {
			t.Errorf("Estimate() on empty = %d, want 0", got)
		}
	})

	t.Run("boundary_zero", func(t *testing.T) {
		s := New(1000)
		if got := s.Estimate(0); got != 0 {
			t.Errorf("Estimate(0) on empty = %d, want 0", got)
		}
	})

	t.Run("boundary_max_uint64", func(t *testing.T) {
		s := New(1000)
		if got := s.Estimate(math.MaxUint64); got != 0 {
			t.Errorf("Estimate(MaxUint64) on empty = %d, want 0", got)
		}
	})

	t.Run("not_added_hash", func(t *testing.T) {
		s := New(1000)
		s.Increment(1)
		// Note: may have false positives, but likely 0
		got := s.Estimate(99999)
		if got > 1 {
			t.Logf("Potential false positive: Estimate(99999) = %d", got)
		}
	})

	t.Run("estimate_after_reset", func(t *testing.T) {
		s := New(1000)
		for i := 0; i < 10; i++ {
			s.Increment(777)
		}
		s.Reset()
		got := s.Estimate(777)
		// 10 >> 1 = 5
		if got != 5 {
			t.Errorf("Estimate() after Reset = %d, want 5", got)
		}
	})
}

// =============================================================================
// Reset Tests
// =============================================================================

func TestReset(t *testing.T) {
	t.Run("happy_reset_halves", func(t *testing.T) {
		s := New(1000)
		for i := 0; i < 8; i++ {
			s.Increment(123)
		}
		s.Reset()
		if got := s.Estimate(123); got != 4 {
			t.Errorf("Estimate() after Reset = %d, want 4", got)
		}
	})

	t.Run("reset_empty", func(t *testing.T) {
		s := New(1000)
		s.Reset() // Should not panic
	})

	t.Run("repeated_reset", func(t *testing.T) {
		s := New(1000)
		for i := 0; i < 12; i++ {
			s.Increment(456)
		}
		s.Reset() // 12 -> 6
		s.Reset() // 6 -> 3
		if got := s.Estimate(456); got != 3 {
			t.Errorf("Estimate() after 2x Reset = %d, want 3", got)
		}
	})

	t.Run("odd_value_reset", func(t *testing.T) {
		s := New(1000)
		for i := 0; i < 5; i++ {
			s.Increment(789)
		}
		s.Reset()
		// 5 >> 1 = 2
		if got := s.Estimate(789); got != 2 {
			t.Errorf("Estimate() after Reset = %d, want 2", got)
		}
	})
}

// =============================================================================
// Clear Tests
// =============================================================================

func TestClear(t *testing.T) {
	t.Run("happy_clear", func(t *testing.T) {
		s := New(1000)
		s.Increment(100)
		s.Increment(200)
		s.Clear()
		if s.Estimate(100) != 0 || s.Estimate(200) != 0 {
			t.Error("Estimate() should be 0 after Clear")
		}
	})

	t.Run("clear_empty", func(t *testing.T) {
		s := New(1000)
		s.Clear() // Should not panic
	})

	t.Run("clear_idempotent", func(t *testing.T) {
		s := New(1000)
		s.Increment(1)
		s.Clear()
		s.Clear()
		if got := s.Estimate(1); got != 0 {
			t.Errorf("Estimate() after 2x Clear = %d, want 0", got)
		}
	})

	t.Run("clear_after_reset", func(t *testing.T) {
		s := New(1000)
		s.Increment(999)
		s.Reset()
		s.Clear()
		if got := s.Estimate(999); got != 0 {
			t.Errorf("Estimate() after Reset+Clear = %d, want 0", got)
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestWorkflow_IncrementEstimateResetEstimate(t *testing.T) {
	s := New(1000)

	// Increment
	for i := 0; i < 10; i++ {
		s.Increment(42)
	}

	// Estimate
	if got := s.Estimate(42); got != 10 {
		t.Errorf("Estimate() = %d, want 10", got)
	}

	// Reset
	s.Reset()

	// Estimate after Reset
	if got := s.Estimate(42); got != 5 {
		t.Errorf("Estimate() after Reset = %d, want 5", got)
	}

	// Clear
	s.Clear()

	// Estimate after Clear
	if got := s.Estimate(42); got != 0 {
		t.Errorf("Estimate() after Clear = %d, want 0", got)
	}
}