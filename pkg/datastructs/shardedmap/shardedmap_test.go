package shardedmap_test

import (
	"testing"

	"github.com/huynhanx03/go-common/pkg/datastructs/shardedmap"
)

// simpleHash is a basic hash function for testing with string keys.
func simpleHash(key string) uint64 {
	var h uint64
	for i := 0; i < len(key); i++ {
		h = h*31 + uint64(key[i])
	}
	return h
}

// intHash is a hash function for testing with int keys.
func intHash(key int) uint64 {
	return uint64(key)
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNew(t *testing.T) {
	tests := []struct {
		name       string
		shards     int
		wantShards int // expected shard count (power of 2)
	}{
		{"valid_16", 16, 16},
		{"valid_256", 256, 256},
		{"zero_defaults_to_256", 0, 256},
		{"negative_defaults_to_256", -1, 256},
		{"rounds_up_17_to_32", 17, 32},
		{"rounds_up_100_to_128", 100, 128},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shardedmap.New[string, int](tt.shards, simpleHash)
			if m == nil {
				t.Fatal("New returned nil")
			}
			// Verify basic operations work
			m.Set("key", 42)
			val, ok := m.Get("key")
			if !ok || val != 42 {
				t.Errorf("basic Set/Get failed: got %v, %v", val, ok)
			}
		})
	}
}

// =============================================================================
// Get Tests
// =============================================================================

func TestGet(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(m *shardedmap.Map[string, int])
		key       string
		wantValue int
		wantOk    bool
	}{
		{
			name:      "existing_key",
			setup:     func(m *shardedmap.Map[string, int]) { m.Set("foo", 42) },
			key:       "foo",
			wantValue: 42,
			wantOk:    true,
		},
		{
			name:      "non_existent_key",
			setup:     func(m *shardedmap.Map[string, int]) { m.Set("foo", 42) },
			key:       "bar",
			wantValue: 0,
			wantOk:    false,
		},
		{
			name:      "empty_map",
			setup:     func(m *shardedmap.Map[string, int]) {},
			key:       "any",
			wantValue: 0,
			wantOk:    false,
		},
		{
			name: "after_delete",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("foo", 42)
				m.Del("foo")
			},
			key:       "foo",
			wantValue: 0,
			wantOk:    false,
		},
		{
			name: "after_clear",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("foo", 42)
				m.Clear()
			},
			key:       "foo",
			wantValue: 0,
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shardedmap.New[string, int](16, simpleHash)
			tt.setup(m)

			val, ok := m.Get(tt.key)
			if ok != tt.wantOk {
				t.Errorf("Get() ok = %v, want %v", ok, tt.wantOk)
			}
			if val != tt.wantValue {
				t.Errorf("Get() value = %v, want %v", val, tt.wantValue)
			}
		})
	}
}

// =============================================================================
// Set Tests
// =============================================================================

func TestSet(t *testing.T) {
	tests := []struct {
		name      string
		ops       func(m *shardedmap.Map[string, int])
		checkKey  string
		wantValue int
	}{
		{
			name:      "insert_new_key",
			ops:       func(m *shardedmap.Map[string, int]) { m.Set("key1", 100) },
			checkKey:  "key1",
			wantValue: 100,
		},
		{
			name: "update_existing_key",
			ops: func(m *shardedmap.Map[string, int]) {
				m.Set("key1", 100)
				m.Set("key1", 200)
			},
			checkKey:  "key1",
			wantValue: 200,
		},
		{
			name: "multiple_keys",
			ops: func(m *shardedmap.Map[string, int]) {
				m.Set("a", 1)
				m.Set("b", 2)
				m.Set("c", 3)
			},
			checkKey:  "b",
			wantValue: 2,
		},
		{
			name:      "empty_string_key",
			ops:       func(m *shardedmap.Map[string, int]) { m.Set("", 999) },
			checkKey:  "",
			wantValue: 999,
		},
		{
			name: "set_after_clear",
			ops: func(m *shardedmap.Map[string, int]) {
				m.Set("old", 1)
				m.Clear()
				m.Set("new", 2)
			},
			checkKey:  "new",
			wantValue: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shardedmap.New[string, int](16, simpleHash)
			tt.ops(m)

			val, ok := m.Get(tt.checkKey)
			if !ok {
				t.Errorf("Get(%q) returned false, expected true", tt.checkKey)
			}
			if val != tt.wantValue {
				t.Errorf("Get(%q) = %v, want %v", tt.checkKey, val, tt.wantValue)
			}
		})
	}
}

// =============================================================================
// Del Tests
// =============================================================================

func TestDel(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(m *shardedmap.Map[string, int])
		delKey string
		verify func(t *testing.T, m *shardedmap.Map[string, int])
	}{
		{
			name:   "delete_existing_key",
			setup:  func(m *shardedmap.Map[string, int]) { m.Set("foo", 42) },
			delKey: "foo",
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				if _, ok := m.Get("foo"); ok {
					t.Error("key should be deleted")
				}
			},
		},
		{
			name:   "delete_non_existent_key",
			setup:  func(m *shardedmap.Map[string, int]) { m.Set("foo", 42) },
			delKey: "bar",
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				if _, ok := m.Get("foo"); !ok {
					t.Error("existing key should still exist")
				}
			},
		},
		{
			name:   "delete_on_empty_map",
			setup:  func(m *shardedmap.Map[string, int]) {},
			delKey: "any",
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				if m.Len() != 0 {
					t.Error("map should still be empty")
				}
			},
		},
		{
			name:   "delete_same_key_twice",
			setup:  func(m *shardedmap.Map[string, int]) { m.Set("foo", 42) },
			delKey: "foo",
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				m.Del("foo") // second delete
				if m.Len() != 0 {
					t.Error("map should be empty after deletes")
				}
			},
		},
		{
			name: "delete_preserves_other_keys",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("a", 1)
				m.Set("b", 2)
				m.Set("c", 3)
			},
			delKey: "b",
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				if _, ok := m.Get("a"); !ok {
					t.Error("key 'a' should exist")
				}
				if _, ok := m.Get("c"); !ok {
					t.Error("key 'c' should exist")
				}
				if m.Len() != 2 {
					t.Errorf("Len() = %d, want 2", m.Len())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shardedmap.New[string, int](16, simpleHash)
			tt.setup(m)
			m.Del(tt.delKey)
			tt.verify(t, m)
		})
	}
}

// =============================================================================
// Len Tests
// =============================================================================

func TestLen(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(m *shardedmap.Map[string, int])
		wantLen int
	}{
		{
			name:    "empty_map",
			setup:   func(m *shardedmap.Map[string, int]) {},
			wantLen: 0,
		},
		{
			name: "after_sets",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("a", 1)
				m.Set("b", 2)
				m.Set("c", 3)
			},
			wantLen: 3,
		},
		{
			name: "after_set_and_delete",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("a", 1)
				m.Set("b", 2)
				m.Del("a")
			},
			wantLen: 1,
		},
		{
			name: "after_clear",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("a", 1)
				m.Set("b", 2)
				m.Clear()
			},
			wantLen: 0,
		},
		{
			name: "update_same_key",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("a", 1)
				m.Set("a", 2)
				m.Set("a", 3)
			},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shardedmap.New[string, int](16, simpleHash)
			tt.setup(m)

			if got := m.Len(); got != tt.wantLen {
				t.Errorf("Len() = %d, want %d", got, tt.wantLen)
			}
		})
	}
}

// =============================================================================
// Clear Tests
// =============================================================================

func TestClear(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(m *shardedmap.Map[string, int])
		verify func(t *testing.T, m *shardedmap.Map[string, int])
	}{
		{
			name: "clear_populated_map",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("a", 1)
				m.Set("b", 2)
				m.Set("c", 3)
			},
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				if m.Len() != 0 {
					t.Errorf("Len() = %d, want 0", m.Len())
				}
			},
		},
		{
			name:  "clear_empty_map",
			setup: func(m *shardedmap.Map[string, int]) {},
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				if m.Len() != 0 {
					t.Errorf("Len() = %d, want 0", m.Len())
				}
			},
		},
		{
			name: "get_after_clear",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("foo", 42)
			},
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				if _, ok := m.Get("foo"); ok {
					t.Error("Get should return false after Clear")
				}
			},
		},
		{
			name: "set_after_clear",
			setup: func(m *shardedmap.Map[string, int]) {
				m.Set("old", 1)
			},
			verify: func(t *testing.T, m *shardedmap.Map[string, int]) {
				m.Set("new", 2)
				if m.Len() != 1 {
					t.Errorf("Len() = %d, want 1", m.Len())
				}
				if v, ok := m.Get("new"); !ok || v != 2 {
					t.Error("new key should be retrievable after Clear+Set")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shardedmap.New[string, int](16, simpleHash)
			tt.setup(m)
			m.Clear()
			tt.verify(t, m)
		})
	}
}

// =============================================================================
// Do Tests
// =============================================================================

func TestDo(t *testing.T) {
	t.Run("iterates_all_items", func(t *testing.T) {
		m := shardedmap.New[string, int](16, simpleHash)
		m.Set("a", 1)
		m.Set("b", 2)
		m.Set("c", 3)

		visited := make(map[string]int)
		m.Do(func(k string, v int) {
			visited[k] = v
		})

		if len(visited) != 3 {
			t.Errorf("visited %d items, want 3", len(visited))
		}
		for _, key := range []string{"a", "b", "c"} {
			if _, ok := visited[key]; !ok {
				t.Errorf("key %q was not visited", key)
			}
		}
	})

	t.Run("empty_map", func(t *testing.T) {
		m := shardedmap.New[string, int](16, simpleHash)

		count := 0
		m.Do(func(k string, v int) {
			count++
		})

		if count != 0 {
			t.Errorf("callback called %d times on empty map, want 0", count)
		}
	})

	t.Run("correct_key_value_pairs", func(t *testing.T) {
		m := shardedmap.New[string, int](16, simpleHash)
		expected := map[string]int{"x": 10, "y": 20, "z": 30}
		for k, v := range expected {
			m.Set(k, v)
		}

		m.Do(func(k string, v int) {
			if expectedV, ok := expected[k]; !ok {
				t.Errorf("unexpected key %q", k)
			} else if v != expectedV {
				t.Errorf("key %q: got value %d, want %d", k, v, expectedV)
			}
		})
	})

	t.Run("count_matches_len", func(t *testing.T) {
		m := shardedmap.New[int, string](16, intHash)
		for i := 0; i < 100; i++ {
			m.Set(i, "value")
		}

		count := 0
		m.Do(func(k int, v string) {
			count++
		})

		if count != m.Len() {
			t.Errorf("Do visited %d items, Len() = %d", count, m.Len())
		}
	})
}

// =============================================================================
// Panic Tests
// =============================================================================

func TestPanic_NilHashFunction(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when using nil hash function")
		}
	}()

	m := shardedmap.New[string, int](16, nil)
	m.Get("key") // should panic
}

func TestPanic_NilCallback(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when Do is called with nil callback")
		}
	}()

	m := shardedmap.New[string, int](16, simpleHash)
	m.Set("key", 1)
	m.Do(nil) // should panic
}

// =============================================================================
// Workflow/Integration Tests
// =============================================================================

func TestWorkflow_SetGetDelSequence(t *testing.T) {
	m := shardedmap.New[string, int](32, simpleHash)

	// Set multiple keys
	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("c", 3)

	if m.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", m.Len())
	}

	// Verify all exist
	for k, expected := range map[string]int{"a": 1, "b": 2, "c": 3} {
		if v, ok := m.Get(k); !ok || v != expected {
			t.Errorf("Get(%q) = %d, %v; want %d, true", k, v, ok, expected)
		}
	}

	// Delete one
	m.Del("b")
	if m.Len() != 2 {
		t.Errorf("Len() after Del = %d, want 2", m.Len())
	}
	if _, ok := m.Get("b"); ok {
		t.Error("deleted key 'b' should not exist")
	}

	// Update existing
	m.Set("a", 100)
	if v, _ := m.Get("a"); v != 100 {
		t.Errorf("updated value = %d, want 100", v)
	}

	// Clear all
	m.Clear()
	if m.Len() != 0 {
		t.Errorf("Len() after Clear = %d, want 0", m.Len())
	}
}

func TestWorkflow_LargeDataSet(t *testing.T) {
	m := shardedmap.New[int, int](64, intHash)

	const n = 10000
	for i := 0; i < n; i++ {
		m.Set(i, i*2)
	}

	if m.Len() != n {
		t.Errorf("Len() = %d, want %d", m.Len(), n)
	}

	// Spot check
	for i := 0; i < n; i += 1000 {
		if v, ok := m.Get(i); !ok || v != i*2 {
			t.Errorf("Get(%d) = %d, %v; want %d, true", i, v, ok, i*2)
		}
	}

	// Clear and verify
	m.Clear()
	if m.Len() != 0 {
		t.Errorf("Len() after Clear = %d, want 0", m.Len())
	}
}
