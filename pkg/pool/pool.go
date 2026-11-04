// Package pool provides calibrated size-class pools for reusable values.
package pool

import (
	"sort"
	"sync"
	"sync/atomic"
)

const (
	minBitSize  = 6
	sizeClasses = 20
	minSize     = 1 << minBitSize
	maxSize     = 1 << (minBitSize + sizeClasses - 1)

	calibrateThreshold = 42000
	percentile95       = 0.95
)

// PoolStats reports Put counts per size class.
type PoolStats [sizeClasses]uint64

// Pool is a concurrency-safe pool grouped by size class.
type Pool[T any] struct {
	calls       [sizeClasses]uint64
	calibrating uint64
	defaultSize uint64
	maxSize     uint64
	buckets     [sizeClasses]sync.Pool
	newFunc     func(size int) T
	sizeFunc    func(T) int
	resetFunc   func(T)
}

// New creates a pool using the supplied creation, sizing, and reset functions.
func New[T any](newFunc func(size int) T, sizeFunc func(T) int, resetFunc func(T)) *Pool[T] {
	p := &Pool[T]{
		newFunc:   newFunc,
		sizeFunc:  sizeFunc,
		resetFunc: resetFunc,
	}
	for i := range p.buckets {
		size := minSize << i
		p.buckets[i].New = func() any {
			return newFunc(size)
		}
	}
	return p
}

// Get returns a value sized for at least size.
func (p *Pool[T]) Get(size int) T {
	if size <= 0 {
		size = minSize
	}

	index := SizeToIndex(size)
	if index >= sizeClasses {
		return p.newFunc(size)
	}

	return p.buckets[index].Get().(T)
}

// Put returns a value to the pool.
func (p *Pool[T]) Put(value T) {
	size := p.sizeFunc(value)
	if size <= 0 {
		return
	}

	index := SizeToIndex(size)
	if index >= sizeClasses {
		return
	}

	if atomic.AddUint64(&p.calls[index], 1) > calibrateThreshold {
		p.calibrate()
	}

	max := int(atomic.LoadUint64(&p.maxSize))
	if max > 0 && size > max {
		return
	}

	if p.resetFunc != nil {
		p.resetFunc(value)
	}
	p.buckets[index].Put(value)
}

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
	stats := make(bucketStats, 0, sizeClasses)
	for i := uint64(0); i < sizeClasses; i++ {
		calls := atomic.SwapUint64(&p.calls[i], 0)
		stats = append(stats, bucket{calls: calls, size: minSize << i})
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
	for _, stat := range stats {
		total += stat.calls
	}
	threshold := uint64(float64(total) * percentile95)

	for _, stat := range stats {
		if sum > threshold {
			break
		}
		sum += stat.calls
		if stat.size > maxSize {
			maxSize = stat.size
		}
	}

	atomic.StoreUint64(&p.defaultSize, defaultSize)
	atomic.StoreUint64(&p.maxSize, maxSize)
}

// DefaultSize returns the most used size class.
func (p *Pool[T]) DefaultSize() uint64 {
	return atomic.LoadUint64(&p.defaultSize)
}

// MaxSize returns the largest retained size class.
func (p *Pool[T]) MaxSize() uint64 {
	return atomic.LoadUint64(&p.maxSize)
}

// GetStats returns Put counts since calibration.
func (p *Pool[T]) GetStats() PoolStats {
	var stats PoolStats
	for i := range p.calls {
		stats[i] = atomic.LoadUint64(&p.calls[i])
	}
	return stats
}

type bucket struct {
	calls uint64
	size  uint64
}

type bucketStats []bucket

func (b bucketStats) Len() int           { return len(b) }
func (b bucketStats) Less(i, j int) bool { return b[i].calls > b[j].calls }
func (b bucketStats) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

// SizeToIndex returns the size-class index for n.
func SizeToIndex(n int) int {
	n--
	n >>= minBitSize
	index := 0
	for n > 0 {
		n >>= 1
		index++
	}
	return index
}

// BucketSize returns a bucket's size, or zero for an invalid index.
func BucketSize(index int) int {
	if index < 0 || index >= sizeClasses {
		return 0
	}
	return minSize << index
}
