package zset

import (
	"strconv"
	"testing"
)

func TestZSet_AddAndGet(t *testing.T) {
	z := New()
	assertTrue(t, z.Add("user1", 100))
	assertFalse(t, z.Add("user1", 200))
	assertEqual(t, 1, z.Card())

	score, ok := z.Score("user1")
	assertTrue(t, ok)
	assertEqual(t, 200.0, score)
}

func TestZSet_Rem(t *testing.T) {
	z := New()
	z.Add("alice", 100)
	z.Add("bob", 200)

	assertTrue(t, z.Rem("alice"))
	assertFalse(t, z.Rem("alice"))
	assertEqual(t, 1, z.Card())
}

func TestZSet_Rank_ASC(t *testing.T) {
	z := New()
	z.Add("a", 100) // rank 0
	z.Add("c", 300) // rank 2
	z.Add("b", 200) // rank 1

	rank, ok := z.Rank("a")
	assertTrue(t, ok)
	assertEqual(t, 0, rank)

	rank, _ = z.Rank("b")
	assertEqual(t, 1, rank)

	rank, _ = z.Rank("c")
	assertEqual(t, 2, rank)
}

func TestZSet_RevRank_DESC(t *testing.T) {
	z := New()
	z.Add("a", 100)
	z.Add("c", 300)
	z.Add("b", 200)

	rank, ok := z.RevRank("c")
	assertTrue(t, ok)
	assertEqual(t, 0, rank) // highest score = RevRank 0

	rank, _ = z.RevRank("a")
	assertEqual(t, 2, rank) // lowest = RevRank 2
}

func TestZSet_Range_ASC(t *testing.T) {
	z := New()
	z.Add("a", 100)
	z.Add("c", 300)
	z.Add("b", 200)

	result := z.Range(0, -1)
	assertEqual(t, 3, len(result))
	assertEqual(t, 100.0, result[0].Score)
	assertEqual(t, 200.0, result[1].Score)
	assertEqual(t, 300.0, result[2].Score)
}

func TestZSet_RevRange_DESC(t *testing.T) {
	z := New()
	z.Add("a", 100)
	z.Add("c", 300)
	z.Add("b", 200)

	result := z.RevRange(0, 1) // Top 2
	assertEqual(t, 2, len(result))
	assertEqual(t, 300.0, result[0].Score)
	assertEqual(t, 200.0, result[1].Score)
}

func TestZSet_CompositeScore_ICPC(t *testing.T) {
	z := New()

	// ICPC: score = solved * 1_000_000 - penalty
	z.Add("user1", 3*1_000_000-900)  // solved=3, penalty=900
	z.Add("user2", 4*1_000_000-1200) // solved=4, penalty=1200
	z.Add("user3", 3*1_000_000-500)  // solved=3, penalty=500

	top := z.RevRange(0, -1)
	assertEqual(t, 3, len(top))
	assertEqual(t, "user2", top[0].Key) // 4 solved
	assertEqual(t, "user3", top[1].Key) // 3 solved, less penalty
	assertEqual(t, "user1", top[2].Key) // 3 solved, more penalty
}

func TestZSet_IncrBy(t *testing.T) {
	z := New()
	z.Add("alice", 100)

	newScore, ok := z.IncrBy("alice", 50)
	assertTrue(t, ok)
	assertEqual(t, 150.0, newScore)

	_, ok = z.IncrBy("bob", 10)
	assertFalse(t, ok)
}

func TestZSet_UpdateScore(t *testing.T) {
	z := New()
	z.Add("a", 100)
	z.Add("b", 200)
	z.Add("c", 300)

	z.Add("a", 400) // Update a to highest

	top := z.RevRange(0, 0)
	assertEqual(t, "a", top[0].Key)
	assertEqual(t, 400.0, top[0].Score)
}

func TestZSet_LargeScale(t *testing.T) {
	z := New()
	n := 10000
	for i := range n {
		z.Add(strconv.Itoa(i), float64(i))
	}
	assertEqual(t, n, z.Card())

	rank, ok := z.Rank(strconv.Itoa(n-1))
	assertTrue(t, ok)
	assertEqual(t, n-1, rank)

	top := z.RevRange(0, 9)
	assertEqual(t, 10, len(top))
	assertEqual(t, float64(n-1), top[0].Score)

	for i := range n {
		z.Rem(strconv.Itoa(i))
	}
	assertEqual(t, 0, z.Card())
}

func TestZSet_NonExistent(t *testing.T) {
	z := New()
	assertFalse(t, z.Rem("x"))
	_, ok := z.Score("x")
	assertFalse(t, ok)
	_, ok = z.Rank("x")
	assertFalse(t, ok)
}

// --- helpers ---

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if e, ok := expected.(int); ok {
		if a, ok := actual.(int); ok && e == a {
			return
		}
	}
	if e, ok := expected.(float64); ok {
		if a, ok := actual.(float64); ok && e == a {
			return
		}
	}
	if e, ok := expected.(bool); ok {
		if a, ok := actual.(bool); ok && e == a {
			return
		}
	}
	if e, ok := expected.(string); ok {
		if a, ok := actual.(string); ok && e == a {
			return
		}
	}
	t.Fatalf("expected %v, got %v", expected, actual)
}

func assertTrue(t *testing.T, v bool) {
	t.Helper()
	if !v {
		t.Fatal("expected true")
	}
}

func assertFalse(t *testing.T, v bool) {
	t.Helper()
	if v {
		t.Fatal("expected false")
	}
}
