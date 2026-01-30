package calibrated

import (
	"sort"
	"sync"
	"sync/atomic"
)

const (
	MinBitSize = 6  // 64 bytes (CPU cache line)
	Steps      = 20 // 64B to 32MB

	MinSize = 1 << MinBitSize
	MaxSize = 1 << (MinBitSize + Steps - 1)

	CalibrateThreshold = 42000
	Percentile95       = 0.95
)

// Pool is a generic calibrated pool with size buckets.
type Pool[T any] struct {
	calls       [Steps]uint64
	calibrating uint64
	defaultSize uint64
	maxSize     uint64
	buckets     [Steps]sync.Pool
	newFunc     func(size int) T
	sizeFunc    func(T) int
	resetFunc   func(T)
}

// New creates a new calibrated pool.
func New[T any](newFunc func(size int) T, sizeFunc func(T) int, resetFunc func(T)) *Pool[T] {
	p := &Pool[T]{
		newFunc:   newFunc,
		sizeFunc:  sizeFunc,
		resetFunc: resetFunc,
	}
	for i := range p.buckets {
		size := MinSize << i
		p.buckets[i].New = func() any {
			return newFunc(size)
		}
	}
	return p
}

// Get returns an item of at least the given size.
func (p *Pool[T]) Get(size int) T {
	if size <= 0 {
		size = MinSize
	}

	idx := SizeToIndex(size)
	if idx >= Steps {
		return p.newFunc(size)
	}

	return p.buckets[idx].Get().(T)
}

// Put returns an item to the pool.
func (p *Pool[T]) Put(item T) {
	size := p.sizeFunc(item)
	if size == 0 {
		return
	}

	idx := SizeToIndex(size)
	if idx >= Steps {
		return
	}

	if atomic.AddUint64(&p.calls[idx], 1) > CalibrateThreshold {
		p.calibrate()
	}

	max := int(atomic.LoadUint64(&p.maxSize))
	if max > 0 && size > max {
		return
	}

	if p.resetFunc != nil {
		p.resetFunc(item)
	}
	p.buckets[idx].Put(item)
}

// calibrate analyzes usage patterns and adjusts default/max sizes.
func (p *Pool[T]) calibrate() {
	if !atomic.CompareAndSwapUint64(&p.calibrating, 0, 1) {
		return
	}
	defer atomic.StoreUint64(&p.calibrating, 0)

	stats := p.collectStats()
	sort.Sort(stats)
	p.updateSizes(stats)
}

func (p *Pool[T]) collectStats() bucketStats {
	stats := make(bucketStats, 0, Steps)
	for i := uint64(0); i < Steps; i++ {
		calls := atomic.SwapUint64(&p.calls[i], 0)
		stats = append(stats, bucket{calls: calls, size: MinSize << i})
	}
	return stats
}

func (p *Pool[T]) updateSizes(stats bucketStats) {
	if len(stats) == 0 {
		return
	}

	defaultSize := stats[0].size
	maxSize := defaultSize

	var total, sum uint64
	for _, s := range stats {
		total += s.calls
	}
	threshold := uint64(float64(total) * Percentile95)

	for _, s := range stats {
		if sum > threshold {
			break
		}
		sum += s.calls
		if s.size > maxSize {
			maxSize = s.size
		}
	}

	atomic.StoreUint64(&p.defaultSize, defaultSize)
	atomic.StoreUint64(&p.maxSize, maxSize)
}

// DefaultSize returns the calibrated default size.
func (p *Pool[T]) DefaultSize() uint64 {
	return atomic.LoadUint64(&p.defaultSize)
}

// MaxSize returns the calibrated max size.
func (p *Pool[T]) MaxSize() uint64 {
	return atomic.LoadUint64(&p.maxSize)
}

// GetStats returns allocation counts per bucket.
func (p *Pool[T]) GetStats() [Steps]uint64 {
	var result [Steps]uint64
	for i := range p.calls {
		result[i] = atomic.LoadUint64(&p.calls[i])
	}
	return result
}

type bucket struct {
	calls uint64
	size  uint64
}

type bucketStats []bucket

func (b bucketStats) Len() int           { return len(b) }
func (b bucketStats) Less(i, j int) bool { return b[i].calls > b[j].calls }
func (b bucketStats) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

// SizeToIndex returns the bucket index for a given size.
func SizeToIndex(n int) int {
	n--
	n >>= MinBitSize
	idx := 0
	for n > 0 {
		n >>= 1
		idx++
	}
	return idx
}

// BucketSize returns the size of bucket at index i.
func BucketSize(i int) int {
	if i < 0 || i >= Steps {
		return 0
	}
	return MinSize << i
}
