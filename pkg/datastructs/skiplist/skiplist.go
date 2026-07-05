package skiplist

import (
	"math/rand"
	"strings"
)

const (
	// maxLevel defines the maximum number of levels a skip list can have.
	// With p=0.5, this supports up to 2^32 elements efficiently.
	maxLevel = 32

	// probability controls the coin-flip threshold for level promotion.
	// 0.5 means each node has a 50% chance to appear in the next level.
	probability = 0.5
)

// Level holds a forward pointer and span for one level of a Node.
type Level struct {
	forward *Node
	span    int // Number of nodes between this node and forward at this level.
}

// Node represents a single element in the skip list.
type Node struct {
	Key      string
	Score    float64
	backward *Node
	levels   []Level
}

// SkipList is a probabilistic data structure for ordered sets.
// It supports O(logN) insert, delete, search, and rank operations.
// Inspired by Redis t_zset.c and memkv skiplist.go.
type SkipList struct {
	head   *Node
	tail   *Node
	length int
	level  int
}

// ScoreRange defines a score range for range queries.
type ScoreRange struct {
	Min, Max     float64
	MinExclusive bool // If true, min is exclusive (>), otherwise inclusive (>=).
	MaxExclusive bool // If true, max is exclusive (<), otherwise inclusive (<=).
}

// containsScore returns true if value is within the score range.
func (sr ScoreRange) containsScore(value float64) bool {
	return sr.gteMin(value) && sr.lteMax(value)
}

// gteMin returns true if value >= min (or > min if exclusive).
func (sr ScoreRange) gteMin(value float64) bool {
	if sr.MinExclusive {
		return value > sr.Min
	}
	return value >= sr.Min
}

// lteMax returns true if value <= max (or < max if exclusive).
func (sr ScoreRange) lteMax(value float64) bool {
	if sr.MaxExclusive {
		return value < sr.Max
	}
	return value <= sr.Max
}

// New creates an empty skip list.
func New() *SkipList {
	sl := &SkipList{
		level: 1,
	}
	sl.head = &Node{
		levels: make([]Level, maxLevel),
	}
	return sl
}

// Len returns the number of elements in the skip list.
func (sl *SkipList) Len() int {
	return sl.length
}

// randomLevel generates a random level using geometric distribution.
func (sl *SkipList) randomLevel() int {
	level := 1
	for rand.Float64() < probability && level < maxLevel {
		level++
	}
	return level
}

// Insert adds a new element with the given score and key.
// If the element already exists (same score and key), it will be duplicated.
// Caller should check existence before inserting if uniqueness is needed.
func (sl *SkipList) Insert(score float64, key string) *Node {
	// update[i] stores the last node visited at level i before the insert position.
	// rank[i] stores the cumulative rank of update[i].
	var update [maxLevel]*Node
	var rank [maxLevel]int

	x := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		if i == sl.level-1 {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}

		for x.levels[i].forward != nil &&
			(x.levels[i].forward.Score < score ||
				(x.levels[i].forward.Score == score &&
					strings.Compare(x.levels[i].forward.Key, key) < 0)) {
			rank[i] += x.levels[i].span
			x = x.levels[i].forward
		}
		update[i] = x
	}

	newLevel := sl.randomLevel()

	// Expand skip list level if needed.
	if newLevel > sl.level {
		for i := sl.level; i < newLevel; i++ {
			rank[i] = 0
			update[i] = sl.head
			update[i].levels[i].span = sl.length
		}
		sl.level = newLevel
	}

	// Create and wire the new node.
	node := &Node{
		Key:    key,
		Score:  score,
		levels: make([]Level, newLevel),
	}

	for i := 0; i < newLevel; i++ {
		node.levels[i].forward = update[i].levels[i].forward
		update[i].levels[i].forward = node
		node.levels[i].span = update[i].levels[i].span - (rank[0] - rank[i])
		update[i].levels[i].span = rank[0] - rank[i] + 1
	}

	// Increment span for levels above the new node's level.
	for i := newLevel; i < sl.level; i++ {
		update[i].levels[i].span++
	}

	// Set backward pointer.
	if update[0] == sl.head {
		node.backward = nil
	} else {
		node.backward = update[0]
	}

	if node.levels[0].forward != nil {
		node.levels[0].forward.backward = node
	} else {
		sl.tail = node
	}

	sl.length++
	return node
}

// deleteNode is the internal helper that removes a node given its update array.
func (sl *SkipList) deleteNode(x *Node, update [maxLevel]*Node) {
	for i := 0; i < sl.level; i++ {
		if update[i].levels[i].forward == x {
			update[i].levels[i].span += x.levels[i].span - 1
			update[i].levels[i].forward = x.levels[i].forward
		} else {
			update[i].levels[i].span--
		}
	}

	if x.levels[0].forward != nil {
		x.levels[0].forward.backward = x.backward
	} else {
		sl.tail = x.backward
	}

	// Shrink level if top levels are now empty.
	for sl.level > 1 && sl.head.levels[sl.level-1].forward == nil {
		sl.level--
	}
	sl.length--
}

// Delete removes the element with the exact score and key.
// Returns true if found and deleted, false otherwise.
func (sl *SkipList) Delete(score float64, key string) bool {
	var update [maxLevel]*Node
	x := sl.head

	for i := sl.level - 1; i >= 0; i-- {
		for x.levels[i].forward != nil &&
			(x.levels[i].forward.Score < score ||
				(x.levels[i].forward.Score == score &&
					strings.Compare(x.levels[i].forward.Key, key) < 0)) {
			x = x.levels[i].forward
		}
		update[i] = x
	}

	x = x.levels[0].forward
	if x != nil && x.Score == score && x.Key == key {
		sl.deleteNode(x, update)
		return true
	}
	return false
}

// Search finds the node with the given key by traversing all nodes at level 0.
// Returns the node and true if found, nil and false otherwise.
// Note: This is O(N). For O(logN) lookup by key, use a dict alongside the skip list.
func (sl *SkipList) Search(key string) (*Node, bool) {
	x := sl.head.levels[0].forward
	for x != nil {
		if x.Key == key {
			return x, true
		}
		x = x.levels[0].forward
	}
	return nil, false
}

// GetRank returns the 1-based rank of the element with the given score and key.
// Returns 0 if the element is not found.
func (sl *SkipList) GetRank(score float64, key string) int {
	x := sl.head
	rank := 0

	for i := sl.level - 1; i >= 0; i-- {
		for x.levels[i].forward != nil &&
			(x.levels[i].forward.Score < score ||
				(x.levels[i].forward.Score == score &&
					strings.Compare(x.levels[i].forward.Key, key) <= 0)) {
			rank += x.levels[i].span
			x = x.levels[i].forward
		}
		if x.Key == key && x.Score == score {
			return rank
		}
	}
	return 0
}

// GetByRank returns the node at the given 1-based rank.
// Returns nil if rank is out of range.
func (sl *SkipList) GetByRank(rank int) *Node {
	if rank <= 0 || rank > sl.length {
		return nil
	}

	x := sl.head
	traversed := 0

	for i := sl.level - 1; i >= 0; i-- {
		for x.levels[i].forward != nil && traversed+x.levels[i].span <= rank {
			traversed += x.levels[i].span
			x = x.levels[i].forward
		}
		if traversed == rank {
			return x
		}
	}
	return nil
}

// RangeByScore returns all nodes whose score falls within the given range.
// Results are returned in ascending score order.
func (sl *SkipList) RangeByScore(sr ScoreRange) []*Node {
	if !sl.inRange(sr) {
		return nil
	}

	// Find the first node in range.
	x := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for x.levels[i].forward != nil && !sr.gteMin(x.levels[i].forward.Score) {
			x = x.levels[i].forward
		}
	}
	x = x.levels[0].forward

	var result []*Node
	for x != nil && sr.lteMax(x.Score) {
		result = append(result, x)
		x = x.levels[0].forward
	}
	return result
}

// Range returns nodes from start to stop (0-based, inclusive).
// Supports negative indices: -1 is the last element, -2 is second to last, etc.
func (sl *SkipList) Range(start, stop int) []*Node {
	// Normalize negative indices.
	if start < 0 {
		start = sl.length + start
	}
	if stop < 0 {
		stop = sl.length + stop
	}

	if start < 0 {
		start = 0
	}
	if stop >= sl.length {
		stop = sl.length - 1
	}
	if start > stop {
		return nil
	}

	// Convert to 1-based rank.
	node := sl.GetByRank(start + 1)
	if node == nil {
		return nil
	}

	count := stop - start + 1
	result := make([]*Node, 0, count)
	for i := 0; i < count && node != nil; i++ {
		result = append(result, node)
		node = node.levels[0].forward
	}
	return result
}

// UpdateScore updates the score of an existing element.
// If the new position is adjacent to the old one, it updates in-place.
// Otherwise it deletes and re-inserts.
func (sl *SkipList) UpdateScore(curScore float64, key string, newScore float64) *Node {
	var update [maxLevel]*Node
	x := sl.head

	for i := sl.level - 1; i >= 0; i-- {
		for x.levels[i].forward != nil &&
			(x.levels[i].forward.Score < curScore ||
				(x.levels[i].forward.Score == curScore &&
					strings.Compare(x.levels[i].forward.Key, key) < 0)) {
			x = x.levels[i].forward
		}
		update[i] = x
	}

	x = x.levels[0].forward
	if x == nil || x.Score != curScore || x.Key != key {
		return nil
	}

	// In-place update if the order is preserved.
	if (x.backward == nil || x.backward.Score < newScore) &&
		(x.levels[0].forward == nil || x.levels[0].forward.Score > newScore) {
		x.Score = newScore
		return x
	}

	sl.deleteNode(x, update)
	return sl.Insert(newScore, key)
}

// inRange returns true if any element could fall within the given score range.
func (sl *SkipList) inRange(sr ScoreRange) bool {
	if sr.Min > sr.Max || (sr.Min == sr.Max && (sr.MinExclusive || sr.MaxExclusive)) {
		return false
	}
	if sl.tail == nil || !sr.gteMin(sl.tail.Score) {
		return false
	}
	first := sl.head.levels[0].forward
	if first == nil || !sr.lteMax(first.Score) {
		return false
	}
	return true
}
