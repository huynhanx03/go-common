package timer

import (
	"sync"
	"sync/atomic"
	"time"
)

// Timer provides the current time as unix nanoseconds (int64).
// Implementations must be safe for concurrent use.
type Timer interface {
	Now() int64
	Stop()
}

// CachedTimer caches the current time and updates it at a fixed interval,
// avoiding expensive syscalls on every read. The time is stored as
// unix nanoseconds (int64) for zero-allocation atomic loads.
type CachedTimer struct {
	now    int64
	step   time.Duration
	ticker *time.Ticker
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewCachedTimer creates a timer that refreshes every step interval.
// Common steps: 500ms for workerpools, 100ms for rate limiters.
func NewCachedTimer(step time.Duration) *CachedTimer {
	t := &CachedTimer{
		now:    time.Now().UnixNano(),
		step:   step,
		ticker: time.NewTicker(step),
		done:   make(chan struct{}),
	}

	t.wg.Add(1)
	go t.run()

	return t
}

func (t *CachedTimer) run() {
	defer t.wg.Done()

	for {
		select {
		case <-t.ticker.C:
			atomic.StoreInt64(&t.now, time.Now().UnixNano())
		case <-t.done:
			t.ticker.Stop()
			return
		}
	}
}

// Now returns the cached current time as unix nanoseconds.
func (t *CachedTimer) Now() int64 {
	return atomic.LoadInt64(&t.now)
}

// Stop halts the background ticker goroutine.
func (t *CachedTimer) Stop() {
	close(t.done)
	t.wg.Wait()
}

// SystemTimer always calls time.Now() — no caching, exact precision.
// Use as fallback when no CachedTimer is injected.
type SystemTimer struct{}

// Now returns the current time as unix nanoseconds via syscall.
func (SystemTimer) Now() int64 {
	return time.Now().UnixNano()
}

// Stop is a no-op for SystemTimer.
func (SystemTimer) Stop() {}
