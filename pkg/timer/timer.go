package timer

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrInvalidStep reports a non-positive refresh interval.
var ErrInvalidStep = errors.New("timer: step must be positive")

// Timer provides the current time as Unix nanoseconds.
// Implementations must be safe for concurrent use.
type Timer interface {
	Now() int64
}

// CachedTimer stores a periodically refreshed timestamp. It owns one
// goroutine and its owner must call Stop when it is no longer needed.
type CachedTimer struct {
	now      atomic.Int64
	ticker   *time.Ticker
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewCachedTimer creates a timer that refreshes at the supplied interval.
func NewCachedTimer(step time.Duration) (*CachedTimer, error) {
	if step <= 0 {
		return nil, ErrInvalidStep
	}

	t := &CachedTimer{
		ticker: time.NewTicker(step),
		stop:   make(chan struct{}),
	}
	t.now.Store(time.Now().UnixNano())

	t.wg.Add(1)
	go t.run()

	return t, nil
}

func (t *CachedTimer) run() {
	defer t.wg.Done()
	defer t.ticker.Stop()

	for {
		select {
		case <-t.ticker.C:
			t.now.Store(time.Now().UnixNano())
		case <-t.stop:
			return
		}
	}
}

// Now returns the most recently observed Unix-nanosecond timestamp.
func (t *CachedTimer) Now() int64 {
	return t.now.Load()
}

// Stop releases the ticker goroutine. It is safe to call concurrently and
// more than once.
func (t *CachedTimer) Stop() {
	t.stopOnce.Do(func() {
		close(t.stop)
	})
	t.wg.Wait()
}

// SystemTimer reads the system clock directly and owns no resources.
type SystemTimer struct{}

// Now returns the current Unix-nanosecond timestamp.
func (SystemTimer) Now() int64 {
	return time.Now().UnixNano()
}
