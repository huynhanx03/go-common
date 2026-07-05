package zset

import (
	"sync"

	"github.com/huynhanx03/go-common/pkg/datastructs/skiplist"
)

// Member represents an element in the sorted set.
type Member struct {
	Key   string
	Score float64
}

// ZSet is a Go implementation of Redis Sorted Set.
// Wraps skiplist.SkipList (with span/width) + hash table for O(1) key lookup.
// Thread-safe via RWMutex.
type ZSet struct {
	mu     sync.RWMutex
	dict   map[string]*skiplist.Node
	sl     *skiplist.SkipList
	length int
}

// New creates an empty ZSet.
func New() *ZSet {
	return &ZSet{
		dict: make(map[string]*skiplist.Node),
		sl:   skiplist.New(),
	}
}

// Add inserts or updates a member with the given score.
// Returns true if new member was inserted, false if existing was updated.
func (z *ZSet) Add(key string, score float64) bool {
	z.mu.Lock()
	defer z.mu.Unlock()

	if existing, ok := z.dict[key]; ok {
		z.sl.UpdateScore(existing.Score, key, score)
		// UpdateScore may return a new node (delete+reinsert).
		if updated, found := z.sl.Search(key); found {
			z.dict[key] = updated
		}
		return false
	}
	n := z.sl.Insert(score, key)
	z.dict[key] = n
	z.length++
	return true
}

// Rem removes a member. Returns true if found and removed.
func (z *ZSet) Rem(key string) bool {
	z.mu.Lock()
	defer z.mu.Unlock()

	n, ok := z.dict[key]
	if !ok {
		return false
	}
	z.sl.Delete(n.Score, key)
	delete(z.dict, key)
	z.length--
	return true
}

// Score returns the score of a member.
func (z *ZSet) Score(key string) (float64, bool) {
	z.mu.RLock()
	defer z.mu.RUnlock()
	n, ok := z.dict[key]
	if !ok {
		return 0, false
	}
	return n.Score, true
}

// Rank returns the 0-based rank in ASC order (lowest score = rank 0).
func (z *ZSet) Rank(key string) (int, bool) {
	z.mu.RLock()
	defer z.mu.RUnlock()
	n, ok := z.dict[key]
	if !ok {
		return 0, false
	}
	rank := z.sl.GetRank(n.Score, key)
	if rank == 0 {
		return 0, false
	}
	return rank - 1, true
}

// RevRank returns the 0-based rank in DESC order (highest score = rank 0).
func (z *ZSet) RevRank(key string) (int, bool) {
	z.mu.RLock()
	defer z.mu.RUnlock()
	n, ok := z.dict[key]
	if !ok {
		return 0, false
	}
	rank := z.sl.GetRank(n.Score, key)
	if rank == 0 {
		return 0, false
	}
	return z.length - rank, true
}

// Range returns members from start to stop (0-based, inclusive) in ASC order.
func (z *ZSet) Range(start, stop int) []Member {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return toMembers(z.sl.Range(start, stop))
}

// RevRange returns members from start to stop (0-based, inclusive) in DESC order.
// RevRange(0, 9) returns the top 10 members by highest score.
func (z *ZSet) RevRange(start, stop int) []Member {
	z.mu.RLock()
	defer z.mu.RUnlock()

	// Normalize negative indices.
	if start < 0 {
		start = z.length + start
	}
	if stop < 0 {
		stop = z.length + stop
	}
	if start < 0 {
		start = 0
	}
	if stop >= z.length {
		stop = z.length - 1
	}
	if start > stop {
		return nil
	}

	// Convert DESC indices to ASC: RevRange(0,2) with length=5 → ASC Range(2,4)
	ascStart := z.length - 1 - stop
	ascStop := z.length - 1 - start
	nodes := z.sl.Range(ascStart, ascStop)

	// Reverse to get DESC order.
	result := make([]Member, len(nodes))
	for i, n := range nodes {
		result[len(nodes)-1-i] = Member{Key: n.Key, Score: n.Score}
	}
	return result
}

// Card returns the number of members.
func (z *ZSet) Card() int {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.length
}

// Contains checks if a key exists.
func (z *ZSet) Contains(key string) bool {
	z.mu.RLock()
	defer z.mu.RUnlock()
	_, ok := z.dict[key]
	return ok
}

// IncrBy increments the score of a member. Returns new score and true if found.
func (z *ZSet) IncrBy(key string, delta float64) (float64, bool) {
	z.mu.Lock()
	defer z.mu.Unlock()

	n, ok := z.dict[key]
	if !ok {
		return 0, false
	}
	newScore := n.Score + delta
	updated := z.sl.UpdateScore(n.Score, key, newScore)
	if updated != nil {
		z.dict[key] = updated
	}
	return newScore, true
}

// ForEach iterates over all members in ASC order.
// Stops if fn returns false.
func (z *ZSet) ForEach(fn func(rank int, key string, score float64) bool) {
	z.mu.RLock()
	defer z.mu.RUnlock()

	n := z.sl.Range(0, z.length-1)
	for i, node := range n {
		if !fn(i, node.Key, node.Score) {
			return
		}
	}
}

func toMembers(nodes []*skiplist.Node) []Member {
	if len(nodes) == 0 {
		return nil
	}
	result := make([]Member, len(nodes))
	for i, n := range nodes {
		result[i] = Member{Key: n.Key, Score: n.Score}
	}
	return result
}
