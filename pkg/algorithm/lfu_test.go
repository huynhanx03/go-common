package algorithm

import (
	"math/rand"
	"testing"
)

func TestSelectLFUVictim(t *testing.T) {
	tests := []struct {
		name       string
		pool       []LFUEntry
		sampleSize int
		wantFound  bool
		wantKey    uint64 // only checked when deterministic (sampleSize >= len)
		checkKey   bool
	}{
		{
			name:      "empty_pool",
			pool:      nil,
			wantFound: false,
		},
		{
			name:       "single_entry",
			pool:       []LFUEntry{{Key: 1, Counter: 10}},
			sampleSize: 5,
			wantFound:  true,
			wantKey:    1,
			checkKey:   true,
		},
		{
			name: "finds_least_frequent",
			pool: []LFUEntry{
				{Key: 1, Counter: 50},
				{Key: 2, Counter: 5}, // lowest
				{Key: 3, Counter: 30},
				{Key: 4, Counter: 20},
				{Key: 5, Counter: 40},
			},
			sampleSize: 10, // covers entire pool
			wantFound:  true,
			wantKey:    2,
			checkKey:   true,
		},
		{
			name:       "default_sample_zero",
			pool:       []LFUEntry{{Key: 1, Counter: 10}},
			sampleSize: 0,
			wantFound:  true,
			wantKey:    1,
			checkKey:   true,
		},
		{
			name:       "default_sample_negative",
			pool:       []LFUEntry{{Key: 1, Counter: 10}},
			sampleSize: -1,
			wantFound:  true,
			wantKey:    1,
			checkKey:   true,
		},
		{
			name: "all_same_counter",
			pool: []LFUEntry{
				{Key: 1, Counter: 5},
				{Key: 2, Counter: 5},
				{Key: 3, Counter: 5},
			},
			sampleSize: 10,
			wantFound:  true,
			// any key is valid, don't check specific key
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			victim, found := SelectLFUVictim(tt.pool, tt.sampleSize)
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if tt.checkKey && victim.Key != tt.wantKey {
				t.Fatalf("key = %d, want %d", victim.Key, tt.wantKey)
			}
		})
	}
}

func TestSelectLFUVictimStatistical(t *testing.T) {
	pool := make([]LFUEntry, 100)
	for i := range pool {
		pool[i] = LFUEntry{Key: uint64(i), Counter: uint8(i + 1)}
	}
	pool[0] = LFUEntry{Key: 0, Counter: 0} // lowest counter by far

	hits := 0
	trials := 1000
	for i := 0; i < trials; i++ {
		victim, _ := SelectLFUVictim(pool, 5)
		if victim.Key == 0 {
			hits++
		}
	}

	if hits == 0 {
		t.Fatal("expected key 0 (lowest counter) to be selected at least once in 1000 trials")
	}
}

func BenchmarkSelectLFUVictim(b *testing.B) {
	pool := make([]LFUEntry, 10000)
	for i := range pool {
		pool[i] = LFUEntry{Key: uint64(i), Counter: uint8(rand.Intn(256))}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SelectLFUVictim(pool, 5)
	}
}
