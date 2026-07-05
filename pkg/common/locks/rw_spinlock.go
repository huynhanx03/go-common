package locks

import (
	"runtime"
	"sync/atomic"

	rt "github.com/huynhanx03/go-common/pkg/runtime"
)

// rwSpinLock is a hybrid read-write spin lock optimized for short critical sections.
//
// State encoding (int32):
//
//	 0       = unlocked
//	 N > 0   = N readers holding the lock
//	-1       = writer holds the lock
//
// Readers can proceed concurrently. A writer must wait for all readers and other writers.
// Uses the same hybrid spin strategy as SpinLock: active PAUSE spin → Gosched backoff.
type rwSpinLock struct {
	state int32
}

// RLock acquires a read lock. Multiple readers can hold it concurrently.
// Blocks while a writer holds the lock.
func (rw *rwSpinLock) RLock() {
	// Phase 1: Active spin with PAUSE.
	for i := 0; i < spinIterations; i++ {
		s := atomic.LoadInt32(&rw.state)
		if s >= 0 && atomic.CompareAndSwapInt32(&rw.state, s, s+1) {
			return
		}
		rt.Procyield(yieldPerSpin)
	}

	// Phase 2: Gosched backoff.
	backoff := 1
	for {
		s := atomic.LoadInt32(&rw.state)
		if s >= 0 && atomic.CompareAndSwapInt32(&rw.state, s, s+1) {
			return
		}
		for i := 0; i < backoff; i++ {
			runtime.Gosched()
		}
		if backoff < maxGoschedBackoff {
			backoff <<= 1
		}
	}
}

// RUnlock releases a read lock.
func (rw *rwSpinLock) RUnlock() {
	if atomic.AddInt32(&rw.state, -1) < 0 {
		panic("locks: RUnlock of unlocked rwSpinLock")
	}
}

// Lock acquires an exclusive write lock. Blocks until all readers and writers release.
func (rw *rwSpinLock) Lock() {
	// Phase 1: Active spin with PAUSE.
	for i := 0; i < spinIterations; i++ {
		if atomic.CompareAndSwapInt32(&rw.state, 0, -1) {
			return
		}
		rt.Procyield(yieldPerSpin)
	}

	// Phase 2: Gosched backoff.
	backoff := 1
	for !atomic.CompareAndSwapInt32(&rw.state, 0, -1) {
		for i := 0; i < backoff; i++ {
			runtime.Gosched()
		}
		if backoff < maxGoschedBackoff {
			backoff <<= 1
		}
	}
}

// Unlock releases the write lock.
func (rw *rwSpinLock) Unlock() {
	if !atomic.CompareAndSwapInt32(&rw.state, -1, 0) {
		panic("locks: Unlock of unlocked rwSpinLock")
	}
}

// RWSpinLocker is the interface for read-write spin locks.
type RWSpinLocker interface {
	RLock()
	RUnlock()
	Lock()
	Unlock()
}

// NewRWSpinLock creates a hybrid read-write spin lock.
// Optimal for <100ns hold times with read-heavy workloads.
func NewRWSpinLock() RWSpinLocker {
	return new(rwSpinLock)
}
