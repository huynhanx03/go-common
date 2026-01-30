package tinylfu

import (
	"math"
	"sync"
)

const maxVictims = 16 // Pre-allocated buffer size

// Controller coordinates frequency counting and eviction sampling.
type Controller[V any] struct {
	sync.Mutex
	freq       *Frequency
	sampler    *Sampler
	maxCost    int64
	victimsBuf []*Item[V] // Pre-allocated victims buffer
}

// NewController creates a new cache controller.
func NewController[V any](maxCost int64, numCounters int64) *Controller[V] {
	c := &Controller[V]{
		freq:       NewFrequency(numCounters),
		sampler:    NewSampler(maxCost),
		maxCost:    maxCost,
		victimsBuf: make([]*Item[V], 0, maxVictims),
	}
	// Pre-allocate victim items
	for i := 0; i < maxVictims; i++ {
		c.victimsBuf = append(c.victimsBuf, &Item[V]{})
	}
	return c
}

// Add attempts to add a key with given cost.
// Returns victims to evict and whether the key was admitted.
func (c *Controller[V]) Add(key uint64, cost int64) ([]*Item[V], bool) {
	c.Lock()
	defer c.Unlock()

	if cost > c.maxCost {
		return nil, false
	}

	if c.sampler.Update(key, cost) {
		return nil, false
	}

	room := c.sampler.RoomLeft(cost)
	if room >= 0 {
		c.sampler.Add(key, cost)
		return nil, true
	}

	incHits := c.freq.Estimate(key)

	// Reuse pre-allocated slice
	victims := c.victimsBuf[:0]
	victimCount := 0

	for room < 0 && victimCount < maxVictims {
		sample := c.sampler.Sample()
		if len(sample) == 0 {
			break
		}

		minKey, minHits, minIdx, minCost := uint64(0), int64(math.MaxInt64), 0, int64(0)
		for i, entry := range sample {
			hits := c.freq.Estimate(entry.key)
			if hits < minHits {
				minKey, minHits, minIdx, minCost = entry.key, hits, i, entry.cost
			}
		}

		if incHits < minHits {
			return victims[:victimCount], false
		}

		c.sampler.Remove(minKey)
		sample[minIdx] = sample[len(sample)-1]
		sample = sample[:len(sample)-1]

		// Reuse pre-allocated item
		if victimCount < len(c.victimsBuf) {
			c.victimsBuf[victimCount].Key = minKey
			c.victimsBuf[victimCount].Cost = minCost
			victims = c.victimsBuf[:victimCount+1]
		}
		victimCount++
		room = c.sampler.RoomLeft(cost)
	}

	c.sampler.Add(key, cost)
	return victims[:victimCount], true
}

// Has returns true if the key is tracked.
func (c *Controller[V]) Has(key uint64) bool {
	c.Lock()
	defer c.Unlock()
	return c.sampler.Has(key)
}

// Del removes a key from tracking.
func (c *Controller[V]) Del(key uint64) {
	c.Lock()
	defer c.Unlock()
	c.sampler.Remove(key)
}

// Cost returns the cost of a key.
func (c *Controller[V]) Cost(key uint64) int64 {
	c.Lock()
	defer c.Unlock()
	return c.sampler.Cost(key)
}

// Consume implements batcher.Consumer to record accesses.
func (c *Controller[V]) Consume(keys []uint64) error {
	c.Lock()
	defer c.Unlock()
	for _, key := range keys {
		c.freq.Record(key)
	}
	return nil
}

// Clear resets all state.
func (c *Controller[V]) Clear() {
	c.Lock()
	defer c.Unlock()
	c.freq.Clear()
	c.sampler.Clear()
}
