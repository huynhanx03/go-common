package topk

import (
	"container/heap"
	"math/rand"
	"time"

	"github.com/huynhanx03/go-common/pkg/hash"
)

const (
	defaultDepth = 7
	defaultDecay = 0.9
)

type node struct {
	fp    uint64
	count uint64
}

// HeavyKeepers implements the HeavyKeepers algorithm for Top-K tracking.
// It is a probabilistic data structure that estimates item frequencies with high accuracy.
type HeavyKeepers struct {
	k       uint32
	width   uint32
	depth   uint32
	decay   float64
	rows    [][]node
	minHeap *topKHeap
	items   map[string]*heapItem
	rnd     *rand.Rand
}

type heapItem struct {
	val   string
	count uint64
	index int // index in min-heap
}

type topKHeap []*heapItem

func (h topKHeap) Len() int           { return len(h) }
func (h topKHeap) Less(i, j int) bool { return h[i].count < h[j].count }
func (h topKHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *topKHeap) Push(x any) {
	n := len(*h)
	item := x.(*heapItem)
	item.index = n
	*h = append(*h, item)
}
func (h *topKHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[0 : n-1]
	return item
}

// New creates a new HeavyKeepers instance.
func New(k uint32, width uint32, depth uint32, decay float64) *HeavyKeepers {
	if depth == 0 {
		depth = defaultDepth
	}
	if decay == 0 {
		decay = defaultDecay
	}

	rows := make([][]node, depth)
	for i := uint32(0); i < depth; i++ {
		rows[i] = make([]node, width)
	}

	return &HeavyKeepers{
		k:       k,
		width:   width,
		depth:   depth,
		decay:   decay,
		rows:    rows,
		minHeap: &topKHeap{},
		items:   make(map[string]*heapItem),
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Add adds an item to the HeavyKeepers structure.
func (hk *HeavyKeepers) Add(item string) {
	fp := hash.Sum64(item)
	var maxCount uint64

	for i := uint32(0); i < hk.depth; i++ {
		// Use a different seed for each row
		idx := hash.Hash64WithSeed(item, uint64(i)) % uint64(hk.width)
		cell := &hk.rows[i][idx]

		if cell.count == 0 {
			cell.fp = fp
			cell.count = 1
			if maxCount < 1 {
				maxCount = 1
			}
		} else if cell.fp == fp {
			cell.count++
			if cell.count > maxCount {
				maxCount = cell.count
			}
		} else {
			if hk.rnd.Float64() < fastDecayPow(hk.decay, cell.count) {
				cell.count--
				if cell.count == 0 {
					cell.fp = fp
					cell.count = 1
					if maxCount < 1 {
						maxCount = 1
					}
				}
			}
		}
	}

	hk.updateHeap(item, maxCount)
}

func (hk *HeavyKeepers) updateHeap(val string, count uint64) {
	// If item is already in heap, update its count
	if item, exists := hk.items[val]; exists {
		if count > item.count {
			item.count = count
			heap.Fix(hk.minHeap, item.index)
		}
		return
	}

	// If heap is not full, add item
	if uint32(hk.minHeap.Len()) < hk.k {
		item := &heapItem{val: val, count: count}
		heap.Push(hk.minHeap, item)
		hk.items[val] = item
		return
	}

	// If count is greater than the smallest in heap, replace it
	if count > (*hk.minHeap)[0].count {
		removed := heap.Pop(hk.minHeap).(*heapItem)
		delete(hk.items, removed.val)

		item := &heapItem{val: val, count: count}
		heap.Push(hk.minHeap, item)
		hk.items[val] = item
	}
}

// Query checks if an item is likely in the Top-K.
func (hk *HeavyKeepers) Query(item string) bool {
	_, exists := hk.items[item]
	return exists
}

// List returns the Top-K items.
func (hk *HeavyKeepers) List() []string {
	res := make([]string, hk.minHeap.Len())
	// Copy to avoid modifying the original heap while iterating if we wanted to sort
	// but for LIST we usually don't need sorting, just the members.
	for i, item := range *hk.minHeap {
		res[i] = item.val
	}
	return res
}

// fastDecayPow computes base^exp using repeated multiplication.
// Faster than math.Pow for small integer exponents typical in decay calculations.
func fastDecayPow(base float64, exp uint64) float64 {
	result := 1.0
	for exp > 0 {
		if exp&1 == 1 {
			result *= base
		}
		base *= base
		exp >>= 1
	}
	return result
}
