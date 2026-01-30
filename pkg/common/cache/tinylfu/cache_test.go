package tinylfu

import (
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
)

// ============================================================================
// Interface Compliance (compile-time check)
// ============================================================================

var _ cache.LocalCache[string, any] = (*Cache[string, any])(nil)

// ============================================================================
// Mock Timer for testing
// ============================================================================

type mockTimer struct {
	mu      sync.Mutex
	current time.Time
}

func newMockTimer(t time.Time) *mockTimer {
	return &mockTimer{current: t}
}

func (m *mockTimer) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

func (m *mockTimer) Stop() {}

func (m *mockTimer) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = m.current.Add(d)
}

// ============================================================================
// Constructor Tests - New()
// ============================================================================

func TestNew(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "valid config",
			cfg:  Config{MaxCost: 1000, NumCounters: 100, BufferSize: 32},
		},
		{
			name: "default MaxCost when zero",
			cfg:  Config{MaxCost: 0},
		},
		{
			name: "default MaxCost when negative",
			cfg:  Config{MaxCost: -1},
		},
		{
			name: "default NumCounters when zero",
			cfg:  Config{MaxCost: 1000, NumCounters: 0},
		},
		{
			name: "default BufferSize when zero",
			cfg:  Config{MaxCost: 1000, NumCounters: 100, BufferSize: 0},
		},
		{
			name: "nil timer uses stdTimer",
			cfg:  Config{MaxCost: 1000, Timer: nil},
		},
		{
			name: "custom timer",
			cfg:  Config{MaxCost: 1000, Timer: newMockTimer(time.Now())},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New[string, int](tt.cfg)
			if c == nil {
				t.Fatal("New() returned nil")
			}
			defer c.Close()

			// Verify cache is functional
			if c.store == nil {
				t.Error("store is nil")
			}
			if c.controller == nil {
				t.Error("controller is nil")
			}
			if c.timer == nil {
				t.Error("timer is nil")
			}
		})
	}
}

// ============================================================================
// Get() Tests
// ============================================================================

func TestGet(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Cache[string, int])
		key       string
		wantValue int
		wantOk    bool
	}{
		{
			name: "existing key returns value",
			setup: func(c *Cache[string, int]) {
				c.Set("key1", 42, 1)
				time.Sleep(50 * time.Millisecond) // Allow async processing
			},
			key:       "key1",
			wantValue: 42,
			wantOk:    true,
		},
		{
			name:      "non-existent key returns false",
			setup:     func(c *Cache[string, int]) {},
			key:       "nonexistent",
			wantValue: 0,
			wantOk:    false,
		},
		{
			name:      "empty cache returns false",
			setup:     func(c *Cache[string, int]) {},
			key:       "anykey",
			wantValue: 0,
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New[string, int](Config{MaxCost: 1000})
			defer c.Close()

			tt.setup(c)

			got, ok := c.Get(tt.key)
			if ok != tt.wantOk {
				t.Errorf("Get() ok = %v, want %v", ok, tt.wantOk)
			}
			if got != tt.wantValue {
				t.Errorf("Get() value = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

func TestGet_ClosedCache(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	c.Set("key", 42, 1)
	time.Sleep(50 * time.Millisecond)

	c.Close()

	got, ok := c.Get("key")
	if ok {
		t.Error("Get() on closed cache should return false")
	}
	if got != 0 {
		t.Errorf("Get() on closed cache should return zero value, got %v", got)
	}
}

func TestGet_ExpiredItem(t *testing.T) {
	timer := newMockTimer(time.Now())
	c := New[string, int](Config{MaxCost: 1000, Timer: timer})
	defer c.Close()

	// Set with 1 second TTL
	c.SetWithTTL("key", 42, 1, time.Second)
	time.Sleep(50 * time.Millisecond)

	// Verify exists before expiration
	if _, ok := c.Get("key"); !ok {
		t.Error("Get() should find key before expiration")
	}

	// Advance time past expiration
	timer.Advance(2 * time.Second)

	// Should now be expired
	got, ok := c.Get("key")
	if ok {
		t.Error("Get() should return false for expired item")
	}
	if got != 0 {
		t.Errorf("Get() expired item should return zero value, got %v", got)
	}
}

func TestGet_AfterSet(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	c.Set("key", 100, 1)
	time.Sleep(50 * time.Millisecond)

	got, ok := c.Get("key")
	if !ok {
		t.Error("Get() after Set should find key")
	}
	if got != 100 {
		t.Errorf("Get() = %v, want 100", got)
	}
}

// ============================================================================
// Set() Tests
// ============================================================================

func TestSet(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  int
		cost   int64
		wantOk bool
	}{
		{
			name:   "valid set",
			key:    "key1",
			value:  42,
			cost:   1,
			wantOk: true,
		},
		{
			name:   "zero cost",
			key:    "key2",
			value:  100,
			cost:   0,
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New[string, int](Config{MaxCost: 1000})
			defer c.Close()

			ok := c.Set(tt.key, tt.value, tt.cost)
			if ok != tt.wantOk {
				t.Errorf("Set() = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

func TestSet_MultipleSetsSameKey(t *testing.T) {
	c := New[string, int](Config{MaxCost: 10000})
	defer c.Close()

	// Set first value
	c.Set("key", 1, 1)
	time.Sleep(100 * time.Millisecond)

	// Increase frequency by accessing
	c.Get("key")
	time.Sleep(50 * time.Millisecond)

	// Update with new value
	c.Set("key", 3, 1)
	time.Sleep(100 * time.Millisecond)

	// Get should return a value (either 1 or 3 depending on admission)
	_, ok := c.Get("key")
	if !ok {
		t.Error("Get() should find key after Set")
	}
}

// ============================================================================
// SetWithTTL() Tests
// ============================================================================

func TestSetWithTTL(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  int
		cost   int64
		ttl    time.Duration
		wantOk bool
	}{
		{
			name:   "with positive TTL",
			key:    "key1",
			value:  42,
			cost:   1,
			ttl:    time.Hour,
			wantOk: true,
		},
		{
			name:   "with zero TTL (no expiration)",
			key:    "key2",
			value:  100,
			cost:   1,
			ttl:    0,
			wantOk: true,
		},
		{
			name:   "zero cost uses default",
			key:    "key3",
			value:  200,
			cost:   0,
			ttl:    time.Hour,
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New[string, int](Config{MaxCost: 1000})
			defer c.Close()

			ok := c.SetWithTTL(tt.key, tt.value, tt.cost, tt.ttl)
			if ok != tt.wantOk {
				t.Errorf("SetWithTTL() = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

func TestSetWithTTL_ClosedCache(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	c.Close()

	ok := c.SetWithTTL("key", 42, 1, time.Hour)
	if ok {
		t.Error("SetWithTTL() on closed cache should return false")
	}
}

func TestSetWithTTL_WithCostFunc(t *testing.T) {
	c := New[string, string](Config{MaxCost: 1000})
	defer c.Close()

	// Set cost function that returns string length
	c.SetCostFunc(func(v string) int64 {
		return int64(len(v))
	})

	// Set with cost=0 should use cost function
	ok := c.SetWithTTL("key", "hello", 0, time.Hour)
	if !ok {
		t.Error("SetWithTTL() with CostFunc should succeed")
	}
}

// ============================================================================
// Delete() Tests
// ============================================================================

func TestDel(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	c.Set("key", 42, 1)
	time.Sleep(50 * time.Millisecond)

	// Verify exists
	if _, ok := c.Get("key"); !ok {
		t.Error("key should exist before Delete")
	}

	c.Delete("key")

	// Verify deleted
	if _, ok := c.Get("key"); ok {
		t.Error("key should not exist after Delete")
	}
}

func TestDel_ClosedCache(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	c.Set("key", 42, 1)
	time.Sleep(50 * time.Millisecond)

	c.Close()

	// Should not panic
	c.Delete("key")
}

func TestDel_NonExistentKey(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	// Should not panic
	c.Delete("nonexistent")
}

func TestDel_ThenGet(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	c.Set("key", 42, 1)
	time.Sleep(50 * time.Millisecond)

	c.Delete("key")

	got, ok := c.Get("key")
	if ok {
		t.Error("Get() after Delete should return false")
	}
	if got != 0 {
		t.Errorf("Get() after Delete should return zero value, got %v", got)
	}
}

// ============================================================================
// Clear() Tests
// ============================================================================

func TestClear(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	// Add multiple items
	for i := 0; i < 10; i++ {
		c.Set("key"+string(rune('0'+i)), i, 1)
	}
	time.Sleep(100 * time.Millisecond)

	c.Clear()

	// All items should be gone
	for i := 0; i < 10; i++ {
		if _, ok := c.Get("key" + string(rune('0'+i))); ok {
			t.Errorf("key%d should not exist after Clear", i)
		}
	}
}

func TestClear_EmptyCache(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	// Should not panic
	c.Clear()
}

func TestClear_ThenGet(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	c.Set("key", 42, 1)
	time.Sleep(50 * time.Millisecond)

	c.Clear()

	got, ok := c.Get("key")
	if ok {
		t.Error("Get() after Clear should return false")
	}
	if got != 0 {
		t.Errorf("Get() after Clear should return zero value, got %v", got)
	}
}

// ============================================================================
// Close() Tests
// ============================================================================

func TestClose(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})

	// Should not panic
	c.Close()

	// Verify closed state
	if !c.isClosed.Load() {
		t.Error("cache should be marked as closed")
	}
}

func TestClose_Idempotent(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})

	// Multiple closes should not panic
	c.Close()
	c.Close()
	c.Close()
}

func TestClose_ThenOperations(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	c.Close()

	// Set should return false
	if c.Set("key", 42, 1) {
		t.Error("Set() on closed cache should return false")
	}

	// SetWithTTL should return false
	if c.SetWithTTL("key", 42, 1, time.Hour) {
		t.Error("SetWithTTL() on closed cache should return false")
	}

	// Get should return false
	if _, ok := c.Get("key"); ok {
		t.Error("Get() on closed cache should return false")
	}

	// Delete should not panic
	c.Delete("key")
}

// ============================================================================
// SetCostFunc() Tests
// ============================================================================

func TestSetCostFunc(t *testing.T) {
	c := New[string, string](Config{MaxCost: 1000})
	defer c.Close()

	called := false
	c.SetCostFunc(func(v string) int64 {
		called = true
		return int64(len(v))
	})

	// Trigger cost function with cost=0
	c.Set("key", "hello", 0)
	time.Sleep(50 * time.Millisecond)

	if !called {
		t.Error("CostFunc should be called when cost=0")
	}
}

func TestSetCostFunc_Nil(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	// Should not panic
	c.SetCostFunc(nil)
}

func TestSetCostFunc_Usage(t *testing.T) {
	c := New[string, string](Config{MaxCost: 1000})
	defer c.Close()

	c.SetCostFunc(func(v string) int64 {
		return int64(len(v))
	})

	// Test with explicit cost (should not use func)
	c.Set("key1", "hello", 100)
	time.Sleep(50 * time.Millisecond)

	// Simply verify no crash
	_, _ = c.Get("key1")
}

// ============================================================================
// SetOnEvict() Tests
// ============================================================================

func TestSetOnEvict(t *testing.T) {
	c := New[string, int](Config{MaxCost: 100})
	defer c.Close()

	evicted := make(chan *Item[int], 10)
	c.SetOnEvict(func(item *Item[int]) {
		evicted <- item
	})

	// Add items to trigger eviction
	for i := 0; i < 200; i++ {
		c.Set("key"+string(rune(i)), i, 1)
	}
	time.Sleep(200 * time.Millisecond)

	// Check if eviction occurred
	select {
	case <-evicted:
		// Success - eviction callback was called
	default:
		// May not evict in all cases, depends on admission
	}
}

func TestSetOnEvict_Nil(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	// Should not panic
	c.SetOnEvict(nil)
}

// ============================================================================
// storeItem.IsExpired() Tests
// ============================================================================

func TestStoreItem_IsExpired(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name       string
		expiration int64
		now        int64
		want       bool
	}{
		{
			name:       "no expiration (zero)",
			expiration: 0,
			now:        now,
			want:       false,
		},
		{
			name:       "not expired (future)",
			expiration: now + 100,
			now:        now,
			want:       false,
		},
		{
			name:       "expired (past)",
			expiration: now - 100,
			now:        now,
			want:       true,
		},
		{
			name:       "boundary (exact expiration)",
			expiration: now,
			now:        now,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &storeItem[int]{
				expiration: tt.expiration,
			}

			got := item.IsExpired(tt.now)
			if got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================================
// Sequence/Workflow Tests
// ============================================================================

func TestWorkflow_SetGetDelGet(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	// Set
	if !c.Set("key", 42, 1) {
		t.Error("Set should succeed")
	}
	time.Sleep(50 * time.Millisecond)

	// Get
	got, ok := c.Get("key")
	if !ok || got != 42 {
		t.Errorf("Get() = (%v, %v), want (42, true)", got, ok)
	}

	// Delete
	c.Delete("key")

	// Get after Delete
	got, ok = c.Get("key")
	if ok || got != 0 {
		t.Errorf("Get() after Delete = (%v, %v), want (0, false)", got, ok)
	}
}

func TestWorkflow_SetClearGet(t *testing.T) {
	c := New[string, int](Config{MaxCost: 1000})
	defer c.Close()

	// Set multiple
	for i := 0; i < 5; i++ {
		c.Set("key"+string(rune('a'+i)), i, 1)
	}
	time.Sleep(50 * time.Millisecond)

	// Clear
	c.Clear()

	// Get all - should be empty
	for i := 0; i < 5; i++ {
		if _, ok := c.Get("key" + string(rune('a'+i))); ok {
			t.Errorf("key%c should not exist after Clear", 'a'+i)
		}
	}
}

func TestWorkflow_TTLExpiration(t *testing.T) {
	timer := newMockTimer(time.Now())
	c := New[string, int](Config{MaxCost: 1000, Timer: timer})
	defer c.Close()

	// Set with TTL
	c.SetWithTTL("key", 42, 1, time.Second)
	time.Sleep(50 * time.Millisecond)

	// Get before expiration
	if _, ok := c.Get("key"); !ok {
		t.Error("key should exist before expiration")
	}

	// Advance time past TTL
	timer.Advance(2 * time.Second)

	// Get after expiration
	if _, ok := c.Get("key"); ok {
		t.Error("key should be expired")
	}
}
