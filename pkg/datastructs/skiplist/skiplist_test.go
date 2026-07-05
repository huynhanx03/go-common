package skiplist

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestInsertAndSearch(t *testing.T) {
	sl := New()

	sl.Insert(1.0, "a")
	sl.Insert(2.0, "b")
	sl.Insert(3.0, "c")

	if sl.Len() != 3 {
		t.Fatalf("expected length 3, got %d", sl.Len())
	}

	node, found := sl.Search("b")
	if !found {
		t.Fatal("expected to find 'b'")
	}
	if node.Score != 2.0 {
		t.Fatalf("expected score 2.0, got %f", node.Score)
	}

	_, found = sl.Search("z")
	if found {
		t.Fatal("did not expect to find 'z'")
	}
}

func TestDelete(t *testing.T) {
	sl := New()

	sl.Insert(1.0, "a")
	sl.Insert(2.0, "b")
	sl.Insert(3.0, "c")

	ok := sl.Delete(2.0, "b")
	if !ok {
		t.Fatal("expected delete to succeed")
	}
	if sl.Len() != 2 {
		t.Fatalf("expected length 2, got %d", sl.Len())
	}

	_, found := sl.Search("b")
	if found {
		t.Fatal("did not expect to find deleted 'b'")
	}

	ok = sl.Delete(99.0, "nonexistent")
	if ok {
		t.Fatal("expected delete of nonexistent to fail")
	}
}

func TestGetRank(t *testing.T) {
	sl := New()

	sl.Insert(10.0, "a")
	sl.Insert(20.0, "b")
	sl.Insert(30.0, "c")
	sl.Insert(40.0, "d")

	tests := []struct {
		score float64
		key   string
		rank  int
	}{
		{10.0, "a", 1},
		{20.0, "b", 2},
		{30.0, "c", 3},
		{40.0, "d", 4},
		{99.0, "z", 0}, // not found
	}

	for _, tt := range tests {
		rank := sl.GetRank(tt.score, tt.key)
		if rank != tt.rank {
			t.Errorf("GetRank(%f, %s) = %d, want %d", tt.score, tt.key, rank, tt.rank)
		}
	}
}

func TestGetByRank(t *testing.T) {
	sl := New()

	sl.Insert(10.0, "a")
	sl.Insert(20.0, "b")
	sl.Insert(30.0, "c")

	node := sl.GetByRank(1)
	if node == nil || node.Key != "a" {
		t.Errorf("rank 1: expected 'a', got %v", node)
	}

	node = sl.GetByRank(3)
	if node == nil || node.Key != "c" {
		t.Errorf("rank 3: expected 'c', got %v", node)
	}

	node = sl.GetByRank(0)
	if node != nil {
		t.Error("rank 0: expected nil")
	}

	node = sl.GetByRank(4)
	if node != nil {
		t.Error("rank 4: expected nil for out of range")
	}
}

func TestRangeByScore(t *testing.T) {
	sl := New()

	for i := 1; i <= 10; i++ {
		sl.Insert(float64(i), fmt.Sprintf("item%d", i))
	}

	nodes := sl.RangeByScore(ScoreRange{Min: 3.0, Max: 7.0})
	if len(nodes) != 5 {
		t.Fatalf("expected 5 nodes in range [3,7], got %d", len(nodes))
	}
	if nodes[0].Key != "item3" || nodes[4].Key != "item7" {
		t.Error("unexpected range boundaries")
	}

	// Exclusive range.
	nodes = sl.RangeByScore(ScoreRange{Min: 3.0, Max: 7.0, MinExclusive: true, MaxExclusive: true})
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes in range (3,7), got %d", len(nodes))
	}
}

func TestRange(t *testing.T) {
	sl := New()

	sl.Insert(1.0, "a")
	sl.Insert(2.0, "b")
	sl.Insert(3.0, "c")
	sl.Insert(4.0, "d")
	sl.Insert(5.0, "e")

	// Normal range.
	nodes := sl.Range(0, 2)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes for Range(0,2), got %d", len(nodes))
	}
	if nodes[0].Key != "a" || nodes[2].Key != "c" {
		t.Error("unexpected range results")
	}

	// Negative index.
	nodes = sl.Range(-2, -1)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes for Range(-2,-1), got %d", len(nodes))
	}
	if nodes[0].Key != "d" || nodes[1].Key != "e" {
		t.Error("unexpected negative range results")
	}

	// Full range.
	nodes = sl.Range(0, -1)
	if len(nodes) != 5 {
		t.Fatalf("expected 5 nodes for Range(0,-1), got %d", len(nodes))
	}
}

func TestUpdateScore(t *testing.T) {
	sl := New()

	sl.Insert(10.0, "a")
	sl.Insert(20.0, "b")
	sl.Insert(30.0, "c")

	node := sl.UpdateScore(20.0, "b", 5.0)
	if node == nil {
		t.Fatal("expected update to succeed")
	}
	if node.Score != 5.0 {
		t.Fatalf("expected score 5.0, got %f", node.Score)
	}

	// 'b' should now be rank 1 (score 5 < 10 < 30).
	rank := sl.GetRank(5.0, "b")
	if rank != 1 {
		t.Fatalf("expected rank 1 after update, got %d", rank)
	}
}

func TestDuplicateScores(t *testing.T) {
	sl := New()

	sl.Insert(1.0, "a")
	sl.Insert(1.0, "b")
	sl.Insert(1.0, "c")

	if sl.Len() != 3 {
		t.Fatalf("expected 3 with duplicate scores, got %d", sl.Len())
	}

	// Elements with same score should be ordered by key lexicographically.
	node := sl.GetByRank(1)
	if node == nil || node.Key != "a" {
		t.Errorf("expected 'a' at rank 1, got %v", node)
	}
	node = sl.GetByRank(2)
	if node == nil || node.Key != "b" {
		t.Errorf("expected 'b' at rank 2, got %v", node)
	}
	node = sl.GetByRank(3)
	if node == nil || node.Key != "c" {
		t.Errorf("expected 'c' at rank 3, got %v", node)
	}
}

func TestEmptySkipList(t *testing.T) {
	sl := New()

	if sl.Len() != 0 {
		t.Fatal("expected empty skip list")
	}

	_, found := sl.Search("anything")
	if found {
		t.Fatal("should not find anything in empty list")
	}

	nodes := sl.Range(0, -1)
	if len(nodes) != 0 {
		t.Fatal("range on empty list should return nil/empty")
	}

	rank := sl.GetRank(1.0, "a")
	if rank != 0 {
		t.Fatal("rank in empty list should be 0")
	}
}

func BenchmarkInsert(b *testing.B) {
	sl := New()
	for i := 0; i < b.N; i++ {
		sl.Insert(rand.Float64()*1000, fmt.Sprintf("key%d", i))
	}
}

func BenchmarkGetRank(b *testing.B) {
	sl := New()
	for i := 0; i < 100000; i++ {
		sl.Insert(float64(i), fmt.Sprintf("key%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := rand.Intn(100000)
		sl.GetRank(float64(idx), fmt.Sprintf("key%d", idx))
	}
}
