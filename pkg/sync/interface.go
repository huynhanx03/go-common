package sync

import (
	"fmt"
	"iter"
)

// MapConfig contains package-owned configuration for a concurrent Map.
type MapConfig struct {
	Presize  int
	GrowOnly bool
}

// MapOption configures a concurrent Map.
type MapOption func(*MapConfig)

// MapStats contains diagnostic statistics for a Map.
type MapStats struct {
	RootBuckets  int
	TotalBuckets int
	EmptyBuckets int
	Capacity     int
	Size         int
	Counter      int
	CounterLen   int
	MinEntries   int
	MaxEntries   int
	TotalGrowths int64
	TotalShrinks int64
}

// ToString formats diagnostic map statistics.
func (s MapStats) ToString() string {
	return fmt.Sprintf("MapStats{\nRootBuckets:  %d\nTotalBuckets: %d\nEmptyBuckets: %d\nCapacity:     %d\nSize:         %d\nCounter:      %d\nCounterLen:   %d\nMinEntries:   %d\nMaxEntries:   %d\nTotalGrowths: %d\nTotalShrinks: %d\n}\n",
		s.RootBuckets, s.TotalBuckets, s.EmptyBuckets, s.Capacity, s.Size,
		s.Counter, s.CounterLen, s.MinEntries, s.MaxEntries, s.TotalGrowths,
		s.TotalShrinks)
}

// Counter is the package-owned contract for a concurrent counter.
type Counter interface {
	Add(int64)
	Inc()
	Dec()
	Value() int64
	Reset()
}

// BoundedQueue is the package-owned contract for a non-blocking FIFO queue.
type BoundedQueue[T any] interface {
	TryEnqueue(T) bool
	TryDequeue() (T, bool)
}

// MPMCQueue is a bounded queue with multiple producers and consumers.
type MPMCQueue[T any] interface{ BoundedQueue[T] }

// SPSCQueue is a bounded queue with one producer and one consumer.
type SPSCQueue[T any] interface{ BoundedQueue[T] }

// UMPSCQueue is a queue with multiple producers and one consumer.
type UMPSCQueue[T any] interface {
	Enqueue(T)
	Dequeue() T
}

// RToken is an opaque token returned by RBMutex read-lock operations.
// Its representation belongs to this package so implementations can change.
type RToken struct {
	value any
}

// RBMutex is a reader-biased read/write mutex.
type RBMutex interface {
	RLock() *RToken
	RUnlock(*RToken)
	TryRLock() (bool, *RToken)
	Lock()
	TryLock() bool
	Unlock()
}

// Map is the package-owned contract for a concurrent map.
type Map[K comparable, V any] interface {
	Load(K) (V, bool)
	Store(K, V)
	LoadOrStore(K, V) (V, bool)
	LoadAndStore(K, V) (V, bool)
	LoadOrCompute(K, func() (V, bool)) (V, bool)
	Compute(K, func(V, bool) (V, ComputeOp)) (V, bool)
	LoadAndDelete(K) (V, bool)
	Delete(K)
	Range(func(K, V) bool)
	All() iter.Seq2[K, V]
	RangeRelaxed(func(K, V) bool)
	AllRelaxed() iter.Seq2[K, V]
	DeleteMatching(func(K, V) (bool, bool)) int
	Clear()
	Size() int
	Stats() MapStats
}
