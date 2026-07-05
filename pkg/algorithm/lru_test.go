package algorithm

import (
	"math/rand"
	"testing"
)

func TestSelectLRUVictimEmptyPool(t *testing.T) {
	_, found := SelectLRUVictim(nil, 5)
	if found {
		t.Fatal("expected no victim from empty pool")
	}
}

func TestSelectLRUVictimSingleEntry(t *testing.T) {
	pool := []LRUEntry{{Key: 1, LastAccess: 100}}
	victim, found := SelectLRUVictim(pool, 5)
	if !found {
		t.Fatal("expected to find victim")
	}
	if victim.Key != 1 {
		t.Fatalf("expected key 1, got %d", victim.Key)
	}
}

func TestSelectLRUVictimFindsOldest(t *testing.T) {
	pool := []LRUEntry{
		{Key: 1, LastAccess: 500},
		{Key: 2, LastAccess: 100}, // Oldest
		{Key: 3, LastAccess: 300},
		{Key: 4, LastAccess: 200},
		{Key: 5, LastAccess: 400},
	}

	// When sample covers entire pool, must always find the oldest.
	victim, found := SelectLRUVictim(pool, 10)
	if !found {
		t.Fatal("expected to find victim")
	}
	if victim.Key != 2 {
		t.Fatalf("expected oldest key 2, got %d", victim.Key)
	}
}

func TestSelectLRUVictimDefaultSampleSize(t *testing.T) {
	pool := []LRUEntry{
		{Key: 1, LastAccess: 100},
	}

	// sampleSize=0 should use default (5).
	victim, found := SelectLRUVictim(pool, 0)
	if !found || victim.Key != 1 {
		t.Fatal("expected to find the single entry with default sample size")
	}
}

func TestSelectLRUVictimStatistical(t *testing.T) {
	// Create a pool where key=0 has the oldest access time.
	// With enough random samples, it should be picked most often.
	pool := make([]LRUEntry, 100)
	for i := range pool {
		pool[i] = LRUEntry{Key: uint64(i), LastAccess: int64(i + 1) * 1000}
	}
	pool[0] = LRUEntry{Key: 0, LastAccess: 1} // Oldest by far

	hits := 0
	trials := 1000
	for i := 0; i < trials; i++ {
		victim, _ := SelectLRUVictim(pool, 5)
		if victim.Key == 0 {
			hits++
		}
	}

	// With pool=100 and sample=5, probability of picking 0 in sample ≈ 1-(99/100)^5 ≈ 4.9%.
	// Over 1000 trials, expect ~49 hits. Allow some variance.
	if hits == 0 {
		t.Fatal("expected key 0 (oldest) to be selected at least once in 1000 trials")
	}
}

func TestSelectLRUVictimNegativeSampleSize(t *testing.T) {
	pool := []LRUEntry{{Key: 1, LastAccess: 100}}

	// sampleSize=-1 should default to 5.
	victim, found := SelectLRUVictim(pool, -1)
	if !found || victim.Key != 1 {
		t.Fatal("expected to find the single entry with negative sample size")
	}
}

func TestSelectLRUVictimAllSameTimestamp(t *testing.T) {
	pool := []LRUEntry{
		{Key: 1, LastAccess: 500},
		{Key: 2, LastAccess: 500},
		{Key: 3, LastAccess: 500},
	}

	// All entries have the same timestamp; any result is valid.
	_, found := SelectLRUVictim(pool, 10)
	if !found {
		t.Fatal("expected to find a victim from non-empty pool")
	}
}

func BenchmarkSelectLRUVictim(b *testing.B) {
	pool := make([]LRUEntry, 10000)
	for i := range pool {
		pool[i] = LRUEntry{Key: uint64(i), LastAccess: rand.Int63()}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SelectLRUVictim(pool, 5)
	}
}
