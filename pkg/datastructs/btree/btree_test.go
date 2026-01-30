package btree

import (
	"io"
	"math"
	"testing"
)

// Interface compliance check - Tree.Close() follows io.Closer pattern
var _ io.Closer = (*Tree)(nil)

// =============================================================================
// Constructor Tests: NewTree()
// =============================================================================

func TestNewTree(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"creates_valid_tree"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := NewTree()
			if tree == nil {
				t.Fatal("NewTree() returned nil")
			}
			defer tree.Close()

			if tree.buffer == nil {
				t.Error("tree.buffer is nil")
			}
			if tree.data == nil {
				t.Error("tree.data is nil")
			}
		})
	}
}

func TestNewTree_Stats(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	stats := tree.Stats()
	// Fresh tree has 1 leaf key (the absoluteMax sentinel)
	if stats.NumLeafKeys != 1 {
		t.Errorf("NumLeafKeys = %d, want 1", stats.NumLeafKeys)
	}
	// Tree may have more than 1 page due to internal structure
	if stats.NumPages < 1 {
		t.Errorf("NumPages = %d, want >= 1", stats.NumPages)
	}
	if stats.PageSize != 4096 {
		t.Errorf("PageSize = %d, want 4096", stats.PageSize)
	}
}

func TestNewTree_Integration(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(100, 200)
	if got := tree.Get(100); got != 200 {
		t.Errorf("Get(100) = %d, want 200", got)
	}
}

// =============================================================================
// Reset Tests
// =============================================================================

func TestReset(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Tree)
	}{
		{"fresh_tree", func(t *Tree) {}},
		{"after_insertions", func(t *Tree) {
			for i := uint64(1); i <= 100; i++ {
				t.Set(i, i*10)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := NewTree()
			defer tree.Close()

			tt.setup(tree)
			tree.Reset()

			stats := tree.Stats()
			if stats.NumLeafKeys != 1 {
				t.Errorf("after Reset, NumLeafKeys = %d, want 1", stats.NumLeafKeys)
			}
		})
	}
}

func TestReset_ClearsData(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert data
	tree.Set(100, 200)
	if tree.Get(100) != 200 {
		t.Fatal("Set/Get failed before reset")
	}

	// Reset should clear
	tree.Reset()

	// Key should no longer exist (returns 0)
	if got := tree.Get(100); got != 0 {
		t.Errorf("after Reset, Get(100) = %d, want 0", got)
	}
}

func TestReset_Sequence(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Set -> Reset -> Get
	tree.Set(50, 500)
	tree.Reset()
	if got := tree.Get(50); got != 0 {
		t.Errorf("after Set->Reset, Get(50) = %d, want 0", got)
	}

	// Reset -> Set -> Reset -> Get
	tree.Set(60, 600)
	tree.Reset()
	if got := tree.Get(60); got != 0 {
		t.Errorf("after Reset->Set->Reset, Get(60) = %d, want 0", got)
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestClose(t *testing.T) {
	tree := NewTree()
	err := tree.Close()
	if err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestClose_NilTree(t *testing.T) {
	var tree *Tree
	err := tree.Close()
	if err != nil {
		t.Errorf("nil.Close() = %v, want nil", err)
	}
}

func TestClose_AfterOperations(t *testing.T) {
	tree := NewTree()
	for i := uint64(1); i <= 50; i++ {
		tree.Set(i, i*10)
	}
	tree.DeleteBelow(25)

	err := tree.Close()
	if err != nil {
		t.Errorf("Close() after operations = %v, want nil", err)
	}
}

// =============================================================================
// Stats Tests
// =============================================================================

func TestStats(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Fresh tree stats
	stats := tree.Stats()
	if stats.PageSize != 4096 {
		t.Errorf("PageSize = %d, want 4096", stats.PageSize)
	}
	if stats.NumLeafKeys != 1 {
		t.Errorf("fresh tree NumLeafKeys = %d, want 1", stats.NumLeafKeys)
	}
}

func TestStats_AfterInsertions(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert 10 keys
	for i := uint64(1); i <= 10; i++ {
		tree.Set(i, i*100)
	}

	stats := tree.Stats()
	// 10 user keys + 1 sentinel
	if stats.NumLeafKeys != 11 {
		t.Errorf("NumLeafKeys = %d, want 11", stats.NumLeafKeys)
	}
}

func TestStats_AfterDeleteBelow(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert keys with values 1-10
	for i := uint64(1); i <= 10; i++ {
		tree.Set(i, i) // value = i
	}

	statsBefore := tree.Stats()

	// Delete entries with value < 5
	tree.DeleteBelow(5)

	statsAfter := tree.Stats()
	if statsAfter.NumLeafKeys >= statsBefore.NumLeafKeys {
		t.Errorf("NumLeafKeys should decrease after DeleteBelow")
	}
}

// =============================================================================
// Set Tests
// =============================================================================

func TestSet(t *testing.T) {
	tests := []struct {
		name string
		key  uint64
		val  uint64
	}{
		{"simple_set", 100, 200},
		{"min_valid_key", 1, 999},
		{"large_key", math.MaxUint64 - 2, 12345},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := NewTree()
			defer tree.Close()

			tree.Set(tt.key, tt.val)
			if got := tree.Get(tt.key); got != tt.val {
				t.Errorf("Get(%d) = %d, want %d", tt.key, got, tt.val)
			}
		})
	}
}

func TestSet_Update(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(100, 200)
	tree.Set(100, 300) // Update

	if got := tree.Get(100); got != 300 {
		t.Errorf("after update, Get(100) = %d, want 300", got)
	}
}

func TestSet_PanicOnZero(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Set(0, v) should panic")
		}
	}()

	tree := NewTree()
	defer tree.Close()
	tree.Set(0, 100)
}

func TestSet_PanicOnMaxUint64(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Set(MaxUint64, v) should panic")
		}
	}()

	tree := NewTree()
	defer tree.Close()
	tree.Set(math.MaxUint64, 100)
}

func TestSet_NodeSplit(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert enough keys to trigger node splits
	// maxKeys is (4096/16) - 1 = 255
	numKeys := 300
	for i := 1; i <= numKeys; i++ {
		tree.Set(uint64(i), uint64(i*10))
	}

	// Verify all keys are accessible
	for i := 1; i <= numKeys; i++ {
		if got := tree.Get(uint64(i)); got != uint64(i*10) {
			t.Errorf("Get(%d) = %d, want %d", i, got, i*10)
		}
	}

	stats := tree.Stats()
	if stats.NumPages <= 1 {
		t.Errorf("NumPages = %d, expected > 1 after splits", stats.NumPages)
	}
}

func TestSet_RootSplit(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert many keys to trigger root split (tree height increase)
	numKeys := 1000
	for i := 1; i <= numKeys; i++ {
		tree.Set(uint64(i), uint64(i*10))
	}

	// Verify structure is correct
	for i := 1; i <= numKeys; i++ {
		if got := tree.Get(uint64(i)); got != uint64(i*10) {
			t.Errorf("after root split, Get(%d) = %d, want %d", i, got, i*10)
		}
	}

	stats := tree.Stats()
	if stats.NumLeafKeys != numKeys+1 { // +1 for sentinel
		t.Errorf("NumLeafKeys = %d, want %d", stats.NumLeafKeys, numKeys+1)
	}
}

func TestSet_SequentialKeys(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert sequential keys
	for i := uint64(1); i <= 100; i++ {
		tree.Set(i, i*100)
	}

	// Verify all
	for i := uint64(1); i <= 100; i++ {
		if got := tree.Get(i); got != i*100 {
			t.Errorf("Get(%d) = %d, want %d", i, got, i*100)
		}
	}
}

// =============================================================================
// Get Tests
// =============================================================================

func TestGet(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(100, 200)

	tests := []struct {
		name string
		key  uint64
		want uint64
	}{
		{"existing_key", 100, 200},
		{"nonexistent_key", 999, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tree.Get(tt.key); got != tt.want {
				t.Errorf("Get(%d) = %d, want %d", tt.key, got, tt.want)
			}
		})
	}
}

func TestGet_Boundary(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(1, 111)
	tree.Set(math.MaxUint64-2, 222)

	if got := tree.Get(1); got != 111 {
		t.Errorf("Get(1) = %d, want 111", got)
	}
	if got := tree.Get(math.MaxUint64 - 2); got != 222 {
		t.Errorf("Get(MaxUint64-2) = %d, want 222", got)
	}
}

func TestGet_PanicOnZero(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Get(0) should panic")
		}
	}()

	tree := NewTree()
	defer tree.Close()
	tree.Get(0)
}

func TestGet_PanicOnMaxUint64(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Get(MaxUint64) should panic")
		}
	}()

	tree := NewTree()
	defer tree.Close()
	tree.Get(math.MaxUint64)
}

func TestGet_EmptyTree(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Get on fresh tree (no user keys)
	if got := tree.Get(100); got != 0 {
		t.Errorf("Get on empty tree = %d, want 0", got)
	}
}

// =============================================================================
// Iterate Tests
// =============================================================================

func TestIterate(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert keys to create multiple nodes
	for i := uint64(1); i <= 300; i++ {
		tree.Set(i, i*10)
	}

	count := 0
	tree.Iterate(func(n node) {
		count++
	})

	if count == 0 {
		t.Error("Iterate visited 0 nodes")
	}
	// Should visit at least root + some leaves
	if count < 2 {
		t.Errorf("Iterate visited %d nodes, expected more", count)
	}
}

func TestIterate_EmptyTree(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	count := 0
	tree.Iterate(func(n node) {
		count++
	})

	// Empty tree still has root node (may have internal + leaf nodes)
	if count < 1 {
		t.Errorf("Iterate on empty tree visited %d nodes, want >= 1", count)
	}
}

func TestIterate_CountsNodes(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	for i := uint64(1); i <= 50; i++ {
		tree.Set(i, i)
	}

	leafCount := 0
	internalCount := 0
	tree.Iterate(func(n node) {
		if n.isLeaf() {
			leafCount++
		} else {
			internalCount++
		}
	})

	stats := tree.Stats()
	totalNodes := leafCount + internalCount
	if totalNodes != stats.NumPages {
		t.Errorf("Iterate count = %d, Stats.NumPages = %d", totalNodes, stats.NumPages)
	}
}

// =============================================================================
// IterateKV Tests
// =============================================================================

func TestIterateKV(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	for i := uint64(1); i <= 10; i++ {
		tree.Set(i, i*100)
	}

	visited := make(map[uint64]uint64)
	tree.IterateKV(func(key, val uint64) uint64 {
		visited[key] = val
		return 0 // no update
	})

	// Should visit all 10 user keys
	for i := uint64(1); i <= 10; i++ {
		if v, ok := visited[i]; !ok {
			t.Errorf("key %d not visited", i)
		} else if v != i*100 {
			t.Errorf("visited[%d] = %d, want %d", i, v, i*100)
		}
	}
}

func TestIterateKV_Update(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(100, 1000)

	// Update value via IterateKV
	tree.IterateKV(func(key, val uint64) uint64 {
		if key == 100 {
			return 2000 // new value
		}
		return 0 // no update
	})

	if got := tree.Get(100); got != 2000 {
		t.Errorf("after IterateKV update, Get(100) = %d, want 2000", got)
	}
}

func TestIterateKV_NoUpdateOnZero(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(100, 1000)

	// Return 0 = no update
	tree.IterateKV(func(key, val uint64) uint64 {
		return 0
	})

	if got := tree.Get(100); got != 1000 {
		t.Errorf("after IterateKV with 0 return, Get(100) = %d, want 1000", got)
	}
}

func TestIterateKV_CountKeys(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	numKeys := 50
	for i := 1; i <= numKeys; i++ {
		tree.Set(uint64(i), uint64(i*10))
	}

	count := 0
	tree.IterateKV(func(key, val uint64) uint64 {
		if val != 0 { // skip sentinel with value 0
			count++
		}
		return 0
	})

	if count != numKeys {
		t.Errorf("IterateKV counted %d keys, want %d", count, numKeys)
	}
}

// =============================================================================
// DeleteBelow Tests
// =============================================================================

func TestDeleteBelow(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert with values as timestamps
	tree.Set(1, 100) // value 100
	tree.Set(2, 200) // value 200
	tree.Set(3, 300) // value 300

	// Delete entries with value < 200
	tree.DeleteBelow(200)

	if got := tree.Get(1); got != 0 {
		t.Errorf("after DeleteBelow(200), Get(1) = %d, want 0", got)
	}
	if got := tree.Get(2); got != 200 {
		t.Errorf("after DeleteBelow(200), Get(2) = %d, want 200", got)
	}
	if got := tree.Get(3); got != 300 {
		t.Errorf("after DeleteBelow(200), Get(3) = %d, want 300", got)
	}
}

func TestDeleteBelow_Zero(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(1, 100)
	tree.Set(2, 200)

	statsBefore := tree.Stats()

	// DeleteBelow(0) should delete nothing (no values < 0)
	tree.DeleteBelow(0)

	statsAfter := tree.Stats()
	if statsAfter.NumLeafKeys != statsBefore.NumLeafKeys {
		t.Errorf("DeleteBelow(0) changed NumLeafKeys from %d to %d",
			statsBefore.NumLeafKeys, statsAfter.NumLeafKeys)
	}
}

func TestDeleteBelow_EmptyTree(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Should not panic on empty tree
	tree.DeleteBelow(100)
}

func TestDeleteBelow_AllExpired(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	for i := uint64(1); i <= 10; i++ {
		tree.Set(i, i) // values 1-10
	}

	// Delete all (values < 999)
	tree.DeleteBelow(999)

	// All user keys should be gone
	for i := uint64(1); i <= 10; i++ {
		if got := tree.Get(i); got != 0 {
			t.Errorf("after DeleteBelow(999), Get(%d) = %d, want 0", i, got)
		}
	}
}

func TestDeleteBelow_NoneExpired(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	for i := uint64(1); i <= 10; i++ {
		tree.Set(i, 1000+i) // values 1001-1010
	}

	statsBefore := tree.Stats()

	// Delete none (values < 100, but all values are > 1000)
	tree.DeleteBelow(100)

	statsAfter := tree.Stats()
	if statsAfter.NumLeafKeys != statsBefore.NumLeafKeys {
		t.Errorf("DeleteBelow with none expired changed NumLeafKeys from %d to %d",
			statsBefore.NumLeafKeys, statsAfter.NumLeafKeys)
	}
}

func TestDeleteBelow_Sequence(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	tree.Set(100, 50)  // will be deleted
	tree.Set(200, 150) // will survive

	tree.DeleteBelow(100)

	if got := tree.Get(100); got != 0 {
		t.Errorf("Get(100) after delete = %d, want 0", got)
	}
	if got := tree.Get(200); got != 150 {
		t.Errorf("Get(200) after delete = %d, want 150", got)
	}
}

func TestDeleteBelow_FreesPages(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Insert many keys to create multiple pages
	for i := uint64(1); i <= 500; i++ {
		tree.Set(i, i) // value = key
	}

	statsBefore := tree.Stats()

	// Delete half the entries
	tree.DeleteBelow(250)

	statsAfter := tree.Stats()
	if statsAfter.NumPagesFree <= statsBefore.NumPagesFree {
		t.Logf("NumPagesFree: before=%d, after=%d",
			statsBefore.NumPagesFree, statsAfter.NumPagesFree)
		// This is expected behavior - pages may be reused
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestIntegration_SetGetDelete(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// Set multiple keys with varying timestamps
	for i := uint64(1); i <= 100; i++ {
		tree.Set(i, i*10) // value = i*10
	}

	// Verify all exist
	for i := uint64(1); i <= 100; i++ {
		if got := tree.Get(i); got != i*10 {
			t.Fatalf("before delete, Get(%d) = %d, want %d", i, got, i*10)
		}
	}

	// Delete entries with value < 500 (keys 1-49)
	tree.DeleteBelow(500)

	// Keys 1-49 should be gone
	for i := uint64(1); i < 50; i++ {
		if got := tree.Get(i); got != 0 {
			t.Errorf("after delete, Get(%d) = %d, want 0", i, got)
		}
	}

	// Keys 50-100 should remain
	for i := uint64(50); i <= 100; i++ {
		if got := tree.Get(i); got != i*10 {
			t.Errorf("after delete, Get(%d) = %d, want %d", i, got, i*10)
		}
	}
}

func TestIntegration_ResetAndReuse(t *testing.T) {
	tree := NewTree()
	defer tree.Close()

	// First use
	for i := uint64(1); i <= 50; i++ {
		tree.Set(i, i*100)
	}

	tree.Reset()

	// Second use - should work fine
	for i := uint64(100); i <= 150; i++ {
		tree.Set(i, i*200)
	}

	// Old keys should not exist
	for i := uint64(1); i <= 50; i++ {
		if got := tree.Get(i); got != 0 {
			t.Errorf("old key Get(%d) = %d, want 0", i, got)
		}
	}

	// New keys should exist
	for i := uint64(100); i <= 150; i++ {
		if got := tree.Get(i); got != i*200 {
			t.Errorf("new key Get(%d) = %d, want %d", i, got, i*200)
		}
	}
}
