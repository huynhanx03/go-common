package tinylfu

const (
	sampleSize = 5
)

// costEntry tracks the cost of a cached key.
type costEntry struct {
	key  uint64
	cost int64
}

// Sampler implements Sampled LFU eviction policy.
// It tracks costs and selects victims using random sampling.
type Sampler struct {
	costs   map[uint64]int64
	maxCost int64
	used    int64
}

// NewSampler creates a new sampler.
func NewSampler(maxCost int64) *Sampler {
	return &Sampler{
		costs:   make(map[uint64]int64),
		maxCost: maxCost,
	}
}

// RoomLeft returns remaining capacity after adding cost.
func (s *Sampler) RoomLeft(cost int64) int64 {
	return s.maxCost - (s.used + cost)
}

// Sample returns a random sample of entries for eviction consideration.
func (s *Sampler) Sample() []*costEntry {
	if len(s.costs) <= sampleSize {
		buf := make([]*costEntry, 0, len(s.costs))
		for key, cost := range s.costs {
			buf = append(buf, &costEntry{key: key, cost: cost})
		}
		return buf
	}

	buf := make([]*costEntry, 0, sampleSize)
	for key, cost := range s.costs {
		buf = append(buf, &costEntry{key: key, cost: cost})
		if len(buf) >= sampleSize {
			break
		}
	}
	return buf
}

// Has returns true if the key is tracked.
func (s *Sampler) Has(key uint64) bool {
	_, ok := s.costs[key]
	return ok
}

// Update updates the cost of an existing key.
func (s *Sampler) Update(key uint64, cost int64) bool {
	if oldCost, ok := s.costs[key]; ok {
		s.used += cost - oldCost
		s.costs[key] = cost
		return true
	}
	return false
}

// Add tracks a new key with its cost.
func (s *Sampler) Add(key uint64, cost int64) {
	if s.Has(key) {
		s.Update(key, cost)
		return
	}
	s.costs[key] = cost
	s.used += cost
}

// Remove stops tracking a key.
func (s *Sampler) Remove(key uint64) {
	if cost, ok := s.costs[key]; ok {
		s.used -= cost
		delete(s.costs, key)
	}
}

// Cost returns the cost of a key, or -1 if not found.
func (s *Sampler) Cost(key uint64) int64 {
	if c, ok := s.costs[key]; ok {
		return c
	}
	return -1
}

// Clear removes all tracking data.
func (s *Sampler) Clear() {
	s.costs = make(map[uint64]int64)
	s.used = 0
}
