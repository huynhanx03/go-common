package tinylfu

import (
	"testing"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewController(t *testing.T) {
	tests := []struct {
		name        string
		maxCost     int64
		numCounters int64
		wantNil     bool
	}{
		{"valid_params", 100, 1000, false},
		{"zero_maxCost", 0, 1000, false},
		{"zero_counters", 100, 0, false},
		{"large_values", 1 << 20, 1 << 20, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController[int](tt.maxCost, tt.numCounters)
			if (c == nil) != tt.wantNil {
				t.Errorf("NewController() = nil: %v, wantNil: %v", c == nil, tt.wantNil)
			}
			if c != nil {
				if c.maxCost != tt.maxCost {
					t.Errorf("maxCost = %d, want %d", c.maxCost, tt.maxCost)
				}
				if c.freq == nil {
					t.Error("freq should not be nil")
				}
				if c.sampler == nil {
					t.Error("sampler should not be nil")
				}
				if len(c.victimsBuf) != maxVictims {
					t.Errorf("victimsBuf len = %d, want %d", len(c.victimsBuf), maxVictims)
				}
			}
		})
	}
}

// =============================================================================
// Add Tests
// =============================================================================

func TestController_Add(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Controller[int])
		key         uint64
		cost        int64
		wantAdmit   bool
		wantVictims int
	}{
		{
			name:        "add_to_empty",
			setup:       func(c *Controller[int]) {},
			key:         1,
			cost:        10,
			wantAdmit:   true,
			wantVictims: 0,
		},
		{
			name: "update_existing_key",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
			},
			key:         1,
			cost:        20,
			wantAdmit:   false, // Update returns false
			wantVictims: 0,
		},
		{
			name:        "cost_exceeds_maxCost",
			setup:       func(c *Controller[int]) {},
			key:         1,
			cost:        200, // maxCost is 100
			wantAdmit:   false,
			wantVictims: 0,
		},
		{
			name:        "zero_cost",
			setup:       func(c *Controller[int]) {},
			key:         1,
			cost:        0,
			wantAdmit:   true,
			wantVictims: 0,
		},
		{
			name:        "exact_maxCost",
			setup:       func(c *Controller[int]) {},
			key:         1,
			cost:        100, // equal to maxCost
			wantAdmit:   true,
			wantVictims: 0,
		},
		{
			name:        "key_zero",
			setup:       func(c *Controller[int]) {},
			key:         0,
			cost:        10,
			wantAdmit:   true,
			wantVictims: 0,
		},
		{
			name: "add_after_clear",
			setup: func(c *Controller[int]) {
				c.Add(1, 50)
				c.Clear()
			},
			key:         2,
			cost:        10,
			wantAdmit:   true,
			wantVictims: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController[int](100, 1000)
			tt.setup(c)

			victims, admitted := c.Add(tt.key, tt.cost)
			if admitted != tt.wantAdmit {
				t.Errorf("Add() admitted = %v, want %v", admitted, tt.wantAdmit)
			}
			if len(victims) != tt.wantVictims {
				t.Errorf("Add() victims = %d, want %d", len(victims), tt.wantVictims)
			}
		})
	}
}

func TestController_Add_Eviction(t *testing.T) {
	// Create controller with small capacity
	c := NewController[int](50, 1000)

	// Fill the cache
	c.Add(1, 30)
	c.Add(2, 20)

	// Record access for key 3 to give it higher frequency
	for i := 0; i < 10; i++ {
		c.Consume([]uint64{3})
	}

	// Now add key 3 which should trigger eviction
	victims, admitted := c.Add(3, 20)

	if !admitted {
		t.Error("expected key 3 to be admitted")
	}
	if len(victims) == 0 {
		t.Error("expected at least one victim when evicting")
	}

	// Verify evicted key is no longer present
	for _, v := range victims {
		if c.Has(v.Key) {
			t.Errorf("victim key %d should not be in cache", v.Key)
		}
	}
}

func TestController_Add_MultipleSequential(t *testing.T) {
	c := NewController[int](100, 1000)

	// Add multiple keys
	keys := []uint64{1, 2, 3, 4, 5}
	for _, key := range keys {
		_, admitted := c.Add(key, 10)
		if !admitted {
			t.Errorf("key %d should be admitted", key)
		}
	}

	// Verify all keys are tracked
	for _, key := range keys {
		if !c.Has(key) {
			t.Errorf("key %d should be present", key)
		}
	}
}

// =============================================================================
// Has Tests
// =============================================================================

func TestController_Has(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Controller[int])
		key    uint64
		expect bool
	}{
		{
			name: "existing_key",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
			},
			key:    1,
			expect: true,
		},
		{
			name:   "non_existing_key",
			setup:  func(c *Controller[int]) {},
			key:    1,
			expect: false,
		},
		{
			name: "after_delete",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
				c.Del(1)
			},
			key:    1,
			expect: false,
		},
		{
			name: "after_clear",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
				c.Clear()
			},
			key:    1,
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController[int](100, 1000)
			tt.setup(c)

			got := c.Has(tt.key)
			if got != tt.expect {
				t.Errorf("Has(%d) = %v, want %v", tt.key, got, tt.expect)
			}
		})
	}
}

// =============================================================================
// Del Tests
// =============================================================================

func TestController_Del(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Controller[int])
		key   uint64
	}{
		{
			name: "delete_existing",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
			},
			key: 1,
		},
		{
			name:  "delete_non_existing",
			setup: func(c *Controller[int]) {},
			key:   1,
		},
		{
			name: "double_delete",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
				c.Del(1) // First delete
			},
			key: 1, // Second delete
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController[int](100, 1000)
			tt.setup(c)

			// Should not panic
			c.Del(tt.key)

			// Key should not exist after delete
			if c.Has(tt.key) {
				t.Errorf("key %d should not exist after Del", tt.key)
			}
		})
	}
}

func TestController_Del_FreesRoom(t *testing.T) {
	c := NewController[int](50, 1000)

	// Fill to capacity
	c.Add(1, 50)

	// Delete key 1
	c.Del(1)

	// Now should have room to add new key
	_, admitted := c.Add(2, 40)
	if !admitted {
		t.Error("should have room after delete")
	}
}

// =============================================================================
// Cost Tests
// =============================================================================

func TestController_Cost(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*Controller[int])
		key      uint64
		wantCost int64
	}{
		{
			name: "existing_key",
			setup: func(c *Controller[int]) {
				c.Add(1, 42)
			},
			key:      1,
			wantCost: 42,
		},
		{
			name:     "non_existing_key",
			setup:    func(c *Controller[int]) {},
			key:      1,
			wantCost: -1,
		},
		{
			name: "zero_cost_key",
			setup: func(c *Controller[int]) {
				c.Add(1, 0)
			},
			key:      1,
			wantCost: 0,
		},
		{
			name: "after_update",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
				c.Add(1, 20) // Update
			},
			key:      1,
			wantCost: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController[int](100, 1000)
			tt.setup(c)

			got := c.Cost(tt.key)
			if got != tt.wantCost {
				t.Errorf("Cost(%d) = %d, want %d", tt.key, got, tt.wantCost)
			}
		})
	}
}

// =============================================================================
// Consume Tests
// =============================================================================

func TestController_Consume(t *testing.T) {
	tests := []struct {
		name    string
		keys    []uint64
		wantErr bool
	}{
		{"valid_keys", []uint64{1, 2, 3}, false},
		{"empty_slice", []uint64{}, false},
		{"nil_slice", nil, false},
		{"single_key", []uint64{1}, false},
		{"duplicate_keys", []uint64{1, 1, 1}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController[int](100, 1000)
			err := c.Consume(tt.keys)
			if (err != nil) != tt.wantErr {
				t.Errorf("Consume() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestController_Consume_AffectsFrequency(t *testing.T) {
	c := NewController[int](100, 1000)

	// Record multiple accesses for key 1
	for i := 0; i < 5; i++ {
		c.Consume([]uint64{1})
	}

	// Add key 1 to cache
	c.Add(1, 10)

	// Key 1 should have higher frequency now
	// We can indirectly verify by checking eviction behavior
	c.Add(2, 90) // Almost fill

	// Key 1 has high frequency, adding key 3 with no frequency
	// should not evict key 1
	c.Add(3, 10) // This might evict key 2 instead of key 1

	if !c.Has(1) {
		t.Error("high frequency key 1 should still be present")
	}
}

// =============================================================================
// Clear Tests
// =============================================================================

func TestController_Clear(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Controller[int])
	}{
		{
			name: "clear_non_empty",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
				c.Add(2, 20)
				c.Consume([]uint64{1, 2, 3})
			},
		},
		{
			name:  "clear_empty",
			setup: func(c *Controller[int]) {},
		},
		{
			name: "clear_after_operations",
			setup: func(c *Controller[int]) {
				c.Add(1, 10)
				c.Del(1)
				c.Add(2, 20)
				c.Consume([]uint64{1, 2})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController[int](100, 1000)
			tt.setup(c)

			// Should not panic
			c.Clear()

			// Verify state is reset
			if c.Has(1) || c.Has(2) {
				t.Error("keys should not exist after Clear")
			}

			// Should be able to add new key
			_, admitted := c.Add(99, 10)
			if !admitted {
				t.Error("should be able to add after Clear")
			}
		})
	}
}

// =============================================================================
// Integration / Workflow Tests
// =============================================================================

func TestController_Workflow_NormalSequence(t *testing.T) {
	c := NewController[int](100, 1000)

	// 1. Add some keys
	c.Add(1, 30)
	c.Add(2, 30)
	c.Add(3, 30)

	// 2. Record accesses
	c.Consume([]uint64{1, 1, 1, 2, 2})

	// 3. Check presence
	if !c.Has(1) || !c.Has(2) || !c.Has(3) {
		t.Error("all keys should be present")
	}

	// 4. Check costs
	if c.Cost(1) != 30 || c.Cost(2) != 30 || c.Cost(3) != 30 {
		t.Error("costs should be 30")
	}

	// 5. Delete one
	c.Del(2)
	if c.Has(2) {
		t.Error("key 2 should be deleted")
	}

	// 6. Clear all
	c.Clear()
	if c.Has(1) || c.Has(3) {
		t.Error("all keys should be cleared")
	}
}

func TestController_Workflow_EvictionPriority(t *testing.T) {
	c := NewController[int](100, 1000)

	// Add keys with high frequency first
	c.Add(1, 50)
	for i := 0; i < 20; i++ {
		c.Consume([]uint64{1})
	}

	// Add key with low frequency
	c.Add(2, 50)

	// Now try to add a new key that requires eviction
	for i := 0; i < 10; i++ {
		c.Consume([]uint64{3})
	}
	victims, admitted := c.Add(3, 50)

	if admitted && len(victims) > 0 {
		// The evicted key should be the low-frequency one (key 2)
		for _, v := range victims {
			if v.Key == 1 {
				t.Error("high frequency key 1 should not be evicted before low frequency key 2")
			}
		}
	}
}
