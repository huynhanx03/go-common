package tinylfu

import (
	"math"
	"testing"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewSampler(t *testing.T) {
	tests := []struct {
		name    string
		maxCost int64
	}{
		{"positive_maxCost", 1000},
		{"zero_maxCost", 0},
		{"negative_maxCost", -1},
		{"max_int64", math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSampler(tt.maxCost)
			if s == nil {
				t.Fatal("NewSampler returned nil")
			}
			if s.maxCost != tt.maxCost {
				t.Errorf("maxCost = %d, want %d", s.maxCost, tt.maxCost)
			}
			if s.used != 0 {
				t.Errorf("used = %d, want 0", s.used)
			}
			if s.costs == nil {
				t.Error("costs map is nil")
			}
			if len(s.costs) != 0 {
				t.Errorf("costs len = %d, want 0", len(s.costs))
			}
		})
	}
}

// =============================================================================
// RoomLeft Tests
// =============================================================================

func TestRoomLeft(t *testing.T) {
	tests := []struct {
		name    string
		maxCost int64
		used    int64
		cost    int64
		want    int64
	}{
		{"empty_sampler", 100, 0, 30, 70},
		{"partially_full", 100, 50, 30, 20},
		{"full_sampler", 100, 100, 10, -10},
		{"negative_cost", 100, 0, -20, 120},
		{"zero_cost", 100, 50, 0, 50},
		{"overfull", 100, 120, 10, -30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSampler(tt.maxCost)
			s.used = tt.used
			got := s.RoomLeft(tt.cost)
			if got != tt.want {
				t.Errorf("RoomLeft(%d) = %d, want %d", tt.cost, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Sample Tests
// =============================================================================

func TestSample(t *testing.T) {
	t.Run("empty_sampler", func(t *testing.T) {
		s := NewSampler(100)
		result := s.Sample()
		if len(result) != 0 {
			t.Errorf("Sample() len = %d, want 0", len(result))
		}
	})

	t.Run("under_sample_size", func(t *testing.T) {
		s := NewSampler(100)
		for i := uint64(1); i <= 3; i++ {
			s.Add(i, int64(i*10))
		}
		result := s.Sample()
		if len(result) != 3 {
			t.Errorf("Sample() len = %d, want 3", len(result))
		}
	})

	t.Run("exact_sample_size", func(t *testing.T) {
		s := NewSampler(100)
		for i := uint64(1); i <= 5; i++ {
			s.Add(i, int64(i*10))
		}
		result := s.Sample()
		if len(result) != 5 {
			t.Errorf("Sample() len = %d, want 5", len(result))
		}
	})

	t.Run("over_sample_size", func(t *testing.T) {
		s := NewSampler(1000)
		for i := uint64(1); i <= 10; i++ {
			s.Add(i, int64(i*10))
		}
		result := s.Sample()
		if len(result) != sampleSize {
			t.Errorf("Sample() len = %d, want %d", len(result), sampleSize)
		}
	})

	t.Run("large_sample", func(t *testing.T) {
		s := NewSampler(10000)
		for i := uint64(1); i <= 100; i++ {
			s.Add(i, int64(i))
		}
		result := s.Sample()
		if len(result) != sampleSize {
			t.Errorf("Sample() len = %d, want %d", len(result), sampleSize)
		}
	})

	t.Run("sampled_keys_exist", func(t *testing.T) {
		s := NewSampler(1000)
		keys := make(map[uint64]int64)
		for i := uint64(1); i <= 20; i++ {
			cost := int64(i * 10)
			s.Add(i, cost)
			keys[i] = cost
		}
		result := s.Sample()
		for _, entry := range result {
			expectedCost, ok := keys[entry.key]
			if !ok {
				t.Errorf("sampled key %d not in original keys", entry.key)
			}
			if entry.cost != expectedCost {
				t.Errorf("sampled cost = %d, want %d", entry.cost, expectedCost)
			}
		}
	})
}

// =============================================================================
// Has Tests
// =============================================================================

func TestHas(t *testing.T) {
	tests := []struct {
		name string
		add  []uint64
		key  uint64
		want bool
	}{
		{"existing_key", []uint64{1, 2, 3}, 2, true},
		{"non_existing_key", []uint64{1, 2, 3}, 5, false},
		{"zero_key_exists", []uint64{0, 1, 2}, 0, true},
		{"max_uint64_not_exists", []uint64{1, 2, 3}, math.MaxUint64, false},
		{"empty_sampler", []uint64{}, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSampler(1000)
			for _, k := range tt.add {
				s.Add(k, 10)
			}
			got := s.Has(tt.key)
			if got != tt.want {
				t.Errorf("Has(%d) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Update Tests
// =============================================================================

func TestUpdate(t *testing.T) {
	t.Run("update_existing_key", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		ok := s.Update(1, 80)
		if !ok {
			t.Error("Update() returned false for existing key")
		}
		if s.costs[1] != 80 {
			t.Errorf("cost = %d, want 80", s.costs[1])
		}
		if s.used != 80 {
			t.Errorf("used = %d, want 80", s.used)
		}
	})

	t.Run("update_non_existing_key", func(t *testing.T) {
		s := NewSampler(1000)
		ok := s.Update(999, 100)
		if ok {
			t.Error("Update() returned true for non-existing key")
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("update_to_zero_cost", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		ok := s.Update(1, 0)
		if !ok {
			t.Error("Update() returned false")
		}
		if s.costs[1] != 0 {
			t.Errorf("cost = %d, want 0", s.costs[1])
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("update_to_negative_cost", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		ok := s.Update(1, -20)
		if !ok {
			t.Error("Update() returned false")
		}
		if s.costs[1] != -20 {
			t.Errorf("cost = %d, want -20", s.costs[1])
		}
		if s.used != -20 {
			t.Errorf("used = %d, want -20", s.used)
		}
	})

	t.Run("update_same_cost", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		ok := s.Update(1, 50)
		if !ok {
			t.Error("Update() returned false")
		}
		if s.used != 50 {
			t.Errorf("used = %d, want 50", s.used)
		}
	})

	t.Run("update_used_calculation", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 30)
		s.Add(2, 40)
		// used = 70
		s.Update(1, 50)
		// used = 70 - 30 + 50 = 90
		if s.used != 90 {
			t.Errorf("used = %d, want 90", s.used)
		}
	})
}

// =============================================================================
// Add Tests
// =============================================================================

func TestAdd(t *testing.T) {
	t.Run("add_new_key", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		if !s.Has(1) {
			t.Error("key not tracked after Add")
		}
		if s.costs[1] != 50 {
			t.Errorf("cost = %d, want 50", s.costs[1])
		}
		if s.used != 50 {
			t.Errorf("used = %d, want 50", s.used)
		}
	})

	t.Run("add_duplicate_key", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		s.Add(1, 80)
		// Add overwrites and should adjust used correctly
		if s.costs[1] != 80 {
			t.Errorf("cost = %d, want 80", s.costs[1])
		}
		// used = 80 (old 50 removed, new 80 added)
		if s.used != 80 {
			t.Errorf("used = %d, want 80", s.used)
		}
	})

	t.Run("add_zero_cost", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 0)
		if !s.Has(1) {
			t.Error("key not tracked after Add with zero cost")
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("add_negative_cost", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, -30)
		if !s.Has(1) {
			t.Error("key not tracked after Add with negative cost")
		}
		if s.used != -30 {
			t.Errorf("used = %d, want -30", s.used)
		}
	})

	t.Run("add_multiple_keys", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 10)
		s.Add(2, 20)
		s.Add(3, 30)
		if s.used != 60 {
			t.Errorf("used = %d, want 60", s.used)
		}
		if len(s.costs) != 3 {
			t.Errorf("costs len = %d, want 3", len(s.costs))
		}
	})
}

// =============================================================================
// Remove Tests
// =============================================================================

func TestRemove(t *testing.T) {
	t.Run("remove_existing_key", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		s.Remove(1)
		if s.Has(1) {
			t.Error("key still exists after Remove")
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("remove_non_existing_key", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		s.Remove(999)
		// Should be no-op
		if s.used != 50 {
			t.Errorf("used = %d, want 50", s.used)
		}
	})

	t.Run("remove_from_empty", func(t *testing.T) {
		s := NewSampler(1000)
		s.Remove(1)
		// Should be no-op
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("remove_last_key", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		s.Remove(1)
		if len(s.costs) != 0 {
			t.Errorf("costs len = %d, want 0", len(s.costs))
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("remove_zero_key", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(0, 50)
		s.Remove(0)
		if s.Has(0) {
			t.Error("key 0 still exists after Remove")
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})
}

// =============================================================================
// Cost Tests
// =============================================================================

func TestCost(t *testing.T) {
	tests := []struct {
		name string
		add  map[uint64]int64
		key  uint64
		want int64
	}{
		{"existing_key", map[uint64]int64{1: 50, 2: 60}, 1, 50},
		{"non_existing_key", map[uint64]int64{1: 50}, 999, -1},
		{"zero_key", map[uint64]int64{0: 100}, 0, 100},
		{"zero_cost", map[uint64]int64{1: 0}, 1, 0},
		{"empty_sampler", map[uint64]int64{}, 1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSampler(1000)
			for k, c := range tt.add {
				s.Add(k, c)
			}
			got := s.Cost(tt.key)
			if got != tt.want {
				t.Errorf("Cost(%d) = %d, want %d", tt.key, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Clear Tests
// =============================================================================

func TestSampler_Clear(t *testing.T) {
	t.Run("clear_populated", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		s.Add(2, 60)
		s.Add(3, 70)
		s.Clear()
		if len(s.costs) != 0 {
			t.Errorf("costs len = %d, want 0", len(s.costs))
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("clear_empty", func(t *testing.T) {
		s := NewSampler(1000)
		s.Clear()
		if len(s.costs) != 0 {
			t.Errorf("costs len = %d, want 0", len(s.costs))
		}
		if s.used != 0 {
			t.Errorf("used = %d, want 0", s.used)
		}
	})

	t.Run("has_after_clear", func(t *testing.T) {
		s := NewSampler(1000)
		s.Add(1, 50)
		s.Clear()
		if s.Has(1) {
			t.Error("Has returned true after Clear")
		}
	})

	t.Run("maxCost_preserved", func(t *testing.T) {
		s := NewSampler(500)
		s.Add(1, 50)
		s.Clear()
		if s.maxCost != 500 {
			t.Errorf("maxCost = %d, want 500", s.maxCost)
		}
	})
}

// =============================================================================
// Sequence/Workflow Tests
// =============================================================================

func TestWorkflow_AddUpdateRemove(t *testing.T) {
	s := NewSampler(1000)

	// Add
	s.Add(1, 50)
	if s.used != 50 {
		t.Errorf("after Add: used = %d, want 50", s.used)
	}
	if !s.Has(1) {
		t.Error("after Add: Has(1) = false")
	}

	// Update
	s.Update(1, 80)
	if s.used != 80 {
		t.Errorf("after Update: used = %d, want 80", s.used)
	}
	if s.Cost(1) != 80 {
		t.Errorf("after Update: Cost(1) = %d, want 80", s.Cost(1))
	}

	// Remove
	s.Remove(1)
	if s.used != 0 {
		t.Errorf("after Remove: used = %d, want 0", s.used)
	}
	if s.Has(1) {
		t.Error("after Remove: Has(1) = true")
	}
}

func TestWorkflow_MultipleOperations(t *testing.T) {
	s := NewSampler(1000)

	// Add multiple keys
	s.Add(1, 100)
	s.Add(2, 200)
	s.Add(3, 300)

	if s.used != 600 {
		t.Errorf("used = %d, want 600", s.used)
	}
	if s.RoomLeft(0) != 400 {
		t.Errorf("RoomLeft = %d, want 400", s.RoomLeft(0))
	}

	// Update one
	s.Update(2, 150)
	if s.used != 550 {
		t.Errorf("after Update: used = %d, want 550", s.used)
	}

	// Remove one
	s.Remove(1)
	if s.used != 450 {
		t.Errorf("after Remove: used = %d, want 450", s.used)
	}

	// Sample
	sample := s.Sample()
	if len(sample) != 2 {
		t.Errorf("Sample len = %d, want 2", len(sample))
	}

	// Clear
	s.Clear()
	if s.used != 0 || len(s.costs) != 0 {
		t.Error("Clear did not reset state")
	}
}
