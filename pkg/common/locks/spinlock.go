package locks

import (
	"runtime"
	"sync"
	"sync/atomic"

	rt "github.com/huynhanx03/go-common/pkg/runtime"
)

// Spin phase tuning constants.
const (
	// spinIterations is the number of CAS attempts with active CPU spin
	// before falling back to Gosched. Matches Go runtime's internal spin count.
	spinIterations = 4

	// yieldPerSpin is the PAUSE cycles per spin iteration.
	// Higher = more CPU burn per spin, but less CAS contention.
	yieldPerSpin = 30

	// maxGoschedBackoff caps the Gosched exponential backoff.
	maxGoschedBackoff = 16
)

type spinLock uint32

// Lock acquires the spin-lock using a hybrid strategy:
//  1. Active spin with CPU PAUSE (Procyield) — optimal for <100ns hold times
//  2. Gosched backoff — fallback when contention is unexpectedly high
func (sl *spinLock) Lock() {
	// Phase 1: Active spin with PAUSE instruction.
	for i := 0; i < spinIterations; i++ {
		if atomic.CompareAndSwapUint32((*uint32)(sl), 0, 1) {
			return
		}
		rt.Procyield(yieldPerSpin)
	}

	// Phase 2: Gosched backoff — yield to scheduler, exponential backoff.
	backoff := 1
	for !atomic.CompareAndSwapUint32((*uint32)(sl), 0, 1) {
		for i := 0; i < backoff; i++ {
			runtime.Gosched()
		}
		if backoff < maxGoschedBackoff {
			backoff <<= 1
		}
	}
}

// Unlock releases the spin-lock.
func (sl *spinLock) Unlock() {
	atomic.StoreUint32((*uint32)(sl), 0)
}

// NewSpinLock instantiates a hybrid spin-lock.
func NewSpinLock() sync.Locker {
	return new(spinLock)
}
