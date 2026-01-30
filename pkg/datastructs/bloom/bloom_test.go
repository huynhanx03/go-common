package bloom

import (
	"encoding/json"
	"math"
	"testing"
)

// Interface Compliance (compile-time check)
var (
	_ json.Marshaler   = (*Bloom)(nil)
	_ json.Unmarshaler = (*Bloom)(nil)
)

// =============================================================================
// Constructor Tests: New()
// =============================================================================

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		capacity uint64
		fpRate   float64
		wantErr  bool
	}{
		// Happy path
		{"valid_standard", 1000, 0.01, false},
		// Error cases
		{"zero_capacity", 0, 0.01, true},
		{"zero_fpRate", 1000, 0, true},
		{"negative_fpRate", 1000, -0.1, true},
		{"fpRate_equals_1", 1000, 1.0, true},
		{"fpRate_greater_than_1", 1000, 1.5, true},
		// Boundary
		{"min_capacity", 1, 0.5, false},
		{"large_capacity", 10_000_000, 0.001, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.capacity, tt.fpRate)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == nil {
				t.Error("New() returned nil without error")
			}
		})
	}
}

// =============================================================================
// Add Tests
// =============================================================================

func TestAdd(t *testing.T) {
	t.Run("happy_add_and_has", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(12345)
		if !bf.Has(12345) {
			t.Error("Has() should return true after Add()")
		}
	})

	t.Run("repeated_add_idempotent", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(1)
		bf.Add(1)
		bf.Add(1)
		if !bf.Has(1) {
			t.Error("Has() should return true after repeated Add()")
		}
	})

	t.Run("boundary_zero", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(0)
		if !bf.Has(0) {
			t.Error("Has(0) should return true after Add(0)")
		}
	})

	t.Run("boundary_max_uint64", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(math.MaxUint64)
		if !bf.Has(math.MaxUint64) {
			t.Error("Has(MaxUint64) should return true after Add(MaxUint64)")
		}
	})

	t.Run("add_after_clear", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(100)
		bf.Clear()
		bf.Add(200)
		if !bf.Has(200) {
			t.Error("Has() should return true after Add() following Clear()")
		}
	})
}

// =============================================================================
// AddIfNotHas Tests
// =============================================================================

func TestAddIfNotHas(t *testing.T) {
	t.Run("add_new_item", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		// Should return false because it wasn't there
		if bf.AddIfNotHas(123) {
			t.Error("AddIfNotHas() should return false for new item")
		}
		// Should be there now
		if !bf.Has(123) {
			t.Error("Has() should be true after AddIfNotHas")
		}
	})

	t.Run("add_existing_item", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(456)
		// Should return true because it was already there
		if !bf.AddIfNotHas(456) {
			t.Error("AddIfNotHas() should return true for existing item")
		}
	})

	t.Run("add_twice", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		if bf.AddIfNotHas(789) {
			t.Error("First AddIfNotHas() should return false")
		}
		if !bf.AddIfNotHas(789) {
			t.Error("Second AddIfNotHas() should return true")
		}
	})
}

// =============================================================================
// Has Tests
// =============================================================================

func TestHas(t *testing.T) {
	t.Run("has_on_added", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(999)
		if !bf.Has(999) {
			t.Error("Has() should return true for added element")
		}
	})

	t.Run("has_on_not_added", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(1)
		// Note: This could be a false positive, but with low probability
		if bf.Has(99999) {
			t.Log("Potential false positive for 99999 (acceptable)")
		}
	})

	t.Run("has_on_empty", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		if bf.Has(12345) {
			t.Error("Has() should return false on empty filter")
		}
	})

	t.Run("has_after_clear", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(100)
		bf.Clear()
		if bf.Has(100) {
			t.Error("Has() should return false after Clear()")
		}
	})

	t.Run("boundary_zero", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		if bf.Has(0) {
			t.Error("Has(0) should return false on empty filter")
		}
	})

	t.Run("boundary_max_uint64", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		if bf.Has(math.MaxUint64) {
			t.Error("Has(MaxUint64) should return false on empty filter")
		}
	})
}

// =============================================================================
// Clear Tests
// =============================================================================

func TestClear(t *testing.T) {
	t.Run("clear_populated", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		for i := uint64(0); i < 100; i++ {
			bf.Add(i)
		}
		bf.Clear()
		for i := uint64(0); i < 100; i++ {
			if bf.Has(i) {
				t.Errorf("Has(%d) should return false after Clear()", i)
			}
		}
	})

	t.Run("clear_empty", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Clear() // Should not panic
	})

	t.Run("size_unchanged_after_clear", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		sizeBefore := bf.TotalSize()
		bf.Add(1)
		bf.Clear()
		sizeAfter := bf.TotalSize()
		if sizeBefore != sizeAfter {
			t.Errorf("TotalSize() changed after Clear(): %d -> %d", sizeBefore, sizeAfter)
		}
	})

	t.Run("repeated_clear", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(1)
		bf.Clear()
		bf.Clear()
		bf.Clear()
		// Should not panic
	})
}

// =============================================================================
// MarshalJSON Tests
// =============================================================================

func TestMarshalJSON(t *testing.T) {
	t.Run("marshal_valid", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(42)
		data, err := bf.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON() error = %v", err)
		}
		if len(data) == 0 {
			t.Error("MarshalJSON() returned empty data")
		}
	})

	t.Run("marshal_empty", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		data, err := bf.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON() error = %v", err)
		}
		if len(data) == 0 {
			t.Error("MarshalJSON() returned empty data for empty filter")
		}
	})

	t.Run("marshal_contains_fields", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(1)
		data, _ := bf.MarshalJSON()
		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("Failed to parse JSON: %v", err)
		}
		if _, ok := parsed["bitset"]; !ok {
			t.Error("JSON missing 'bitset' field")
		}
		if _, ok := parsed["k"]; !ok {
			t.Error("JSON missing 'k' field")
		}
		if _, ok := parsed["m"]; !ok {
			t.Error("JSON missing 'm' field")
		}
	})
}

// =============================================================================
// UnmarshalJSON Tests
// =============================================================================

func TestUnmarshalJSON(t *testing.T) {
	t.Run("unmarshal_valid", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(42)
		data, _ := bf.MarshalJSON()

		bf2 := &Bloom{}
		if err := bf2.UnmarshalJSON(data); err != nil {
			t.Errorf("UnmarshalJSON() error = %v", err)
		}
	})

	t.Run("unmarshal_invalid", func(t *testing.T) {
		bf := &Bloom{}
		err := bf.UnmarshalJSON([]byte("invalid json"))
		if err == nil {
			t.Error("UnmarshalJSON() should return error for invalid JSON")
		}
	})

	t.Run("unmarshal_empty", func(t *testing.T) {
		bf := &Bloom{}
		err := bf.UnmarshalJSON([]byte{})
		if err == nil {
			t.Error("UnmarshalJSON() should return error for empty bytes")
		}
	})

	t.Run("roundtrip_preserves_has", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(42)
		bf.Add(100)
		bf.Add(999)

		data, _ := bf.MarshalJSON()

		bf2 := &Bloom{}
		_ = bf2.UnmarshalJSON(data)

		if !bf2.Has(42) {
			t.Error("Roundtrip: Has(42) should return true")
		}
		if !bf2.Has(100) {
			t.Error("Roundtrip: Has(100) should return true")
		}
		if !bf2.Has(999) {
			t.Error("Roundtrip: Has(999) should return true")
		}
	})
}

// =============================================================================
// TotalSize Tests
// =============================================================================

func TestTotalSize(t *testing.T) {
	t.Run("size_greater_than_zero", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		if bf.TotalSize() == 0 {
			t.Error("TotalSize() should be > 0")
		}
	})

	t.Run("size_unchanged_after_add", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		sizeBefore := bf.TotalSize()
		bf.Add(1)
		bf.Add(2)
		bf.Add(3)
		sizeAfter := bf.TotalSize()
		if sizeBefore != sizeAfter {
			t.Errorf("TotalSize() changed after Add(): %d -> %d", sizeBefore, sizeAfter)
		}
	})

	t.Run("size_unchanged_after_clear", func(t *testing.T) {
		bf, _ := New(1000, 0.01)
		bf.Add(1)
		sizeBefore := bf.TotalSize()
		bf.Clear()
		sizeAfter := bf.TotalSize()
		if sizeBefore != sizeAfter {
			t.Errorf("TotalSize() changed after Clear(): %d -> %d", sizeBefore, sizeAfter)
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestWorkflow_AddHasClearHas(t *testing.T) {
	bf, err := New(1000, 0.01)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Add elements
	bf.Add(1)
	bf.Add(2)
	bf.Add(3)

	// Verify Has
	if !bf.Has(1) || !bf.Has(2) || !bf.Has(3) {
		t.Error("Has() should return true for added elements")
	}

	// Clear
	bf.Clear()

	// Verify Has after Clear
	if bf.Has(1) || bf.Has(2) || bf.Has(3) {
		t.Error("Has() should return false after Clear()")
	}
}
