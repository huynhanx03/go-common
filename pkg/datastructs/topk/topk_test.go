package topk

import (
	"math"
	"testing"

	"github.com/huynhanx03/go-common/pkg/hash"
)

func TestTopKPromotesHeavyItemsEvictsWeakItemsAndReturnsCopy(t *testing.T) {
	const width = 4096
	items := distinctBuckets(width, 3)
	weak, middle, heavy := items[0], items[1], items[2]
	tracker := New(2, width, 3, 0.9)
	tracker.Add(weak)
	for range 2 {
		tracker.Add(middle)
	}
	for range 20 {
		tracker.Add(heavy)
	}
	if tracker.Query(weak) || !tracker.Query(middle) || !tracker.Query(heavy) {
		t.Fatal("top-K membership did not promote frequent items and evict the weakest")
	}

	listed := tracker.List()
	listed[0] = "modified"
	if tracker.Query("modified") || !tracker.Query(heavy) {
		t.Fatal("List exposed mutable tracker membership")
	}
}

func TestTopKRejectsInvalidDimensionsAndDecay(t *testing.T) {
	for _, input := range [][4]float64{{0, 1, 1, 0.9}, {1, 0, 1, 0.9}, {1, 1, 1, -0.1}, {1, 1, 1, 1.1}, {1, 1, 1, math.NaN()}, {1, 1, 1, math.Inf(1)}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("accepted invalid construction parameters %v", input)
				}
			}()
			New(uint32(input[0]), uint32(input[1]), uint32(input[2]), input[3])
		}()
	}
	defaults := New(1, 8, 0, 0)
	defaults.Add("single item")
	if !defaults.Query("single item") {
		t.Fatal("zero depth and decay did not use usable defaults")
	}
}

func TestTopKUsesDecayWhenItemsCollideInEveryRow(t *testing.T) {
	tracker := New(1, 1, 1, 1)
	for range 4 {
		tracker.Add("established")
	}
	tracker.Add("collision")
	if !tracker.Query("established") || tracker.Query("collision") {
		t.Fatalf("collision changed tracked membership: %v", tracker.List())
	}
}

func distinctBuckets(width uint32, count int) []string {
	items := make([]string, 0, count)
	used := make([]map[uint64]struct{}, 3)
	for row := range used {
		used[row] = make(map[uint64]struct{})
	}
	for candidate := 0; len(items) < count; candidate++ {
		item := string(rune('a'+candidate%26)) + string(rune('A'+candidate/26))
		positions := [3]uint64{}
		unique := true
		for row := range positions {
			positions[row] = hash.Sum64WithSeed(item, uint64(row)) % uint64(width)
			if _, exists := used[row][positions[row]]; exists {
				unique = false
				break
			}
		}
		if !unique {
			continue
		}
		for row, position := range positions {
			used[row][position] = struct{}{}
		}
		items = append(items, item)
	}
	return items
}
