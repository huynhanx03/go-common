package algorithm

import (
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

// Sliding window defaults.
const (
	defaultWindowSize  = time.Minute
	defaultWindowLimit = 60
)

// SlidingWindow implements a sliding window rate counter.
// It uses two adjacent fixed windows and interpolates the count
// to approximate a true sliding window without storing every timestamp.
type SlidingWindow struct {
	mu        sync.Locker
	limit     int
	window    int64 // window size in nanoseconds
	currStart int64 // window start in unix nanoseconds
	currCount int
	prevCount int
	now       func() int64
}

// SlidingWindowOption configures a SlidingWindow.
type SlidingWindowOption func(*SlidingWindow)

// WithWindowSize sets the window duration.
func WithWindowSize(d time.Duration) SlidingWindowOption {
	return func(sw *SlidingWindow) {
		if d > 0 {
			sw.window = int64(d)
		}
	}
}

// WithWindowLimit sets the maximum allowed events per window.
func WithWindowLimit(n int) SlidingWindowOption {
	return func(sw *SlidingWindow) {
		if n > 0 {
			sw.limit = n
		}
	}
}

// WithWindowClock overrides the time source (unix nanoseconds).
func WithWindowClock(fn func() int64) SlidingWindowOption {
	return func(sw *SlidingWindow) {
		if fn != nil {
			sw.now = fn
		}
	}
}

// NewSlidingWindow creates a sliding window counter with the given options.
func NewSlidingWindow(opts ...SlidingWindowOption) *SlidingWindow {
	sw := &SlidingWindow{
		mu:     locks.NewSpinLock(),
		limit:  defaultWindowLimit,
		window: int64(defaultWindowSize),
		now:    func() int64 { return time.Now().UnixNano() },
	}
	for _, opt := range opts {
		opt(sw)
	}
	// Truncate to window boundary.
	sw.currStart = sw.now() / sw.window * sw.window
	return sw
}

// Allow checks if an event is within the rate limit.
// Returns true and records the event if allowed, false otherwise.
func (sw *SlidingWindow) Allow() bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	sw.advance()

	estimated := sw.estimate()
	if estimated >= float64(sw.limit) {
		return false
	}

	sw.currCount++
	return true
}

// Count returns the current estimated event count in the sliding window.
func (sw *SlidingWindow) Count() float64 {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.advance()
	return sw.estimate()
}

// Reset clears all counters.
func (sw *SlidingWindow) Reset() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.currCount = 0
	sw.prevCount = 0
	sw.currStart = sw.now() / sw.window * sw.window
}

// advance rolls the window forward if the current window has elapsed.
func (sw *SlidingWindow) advance() {
	now := sw.now()
	windowEnd := sw.currStart + sw.window

	if now < windowEnd {
		return
	}

	// How many full windows have passed?
	elapsed := now - sw.currStart
	periods := elapsed / sw.window

	if periods == 1 {
		// Rolled exactly one window: previous = current
		sw.prevCount = sw.currCount
		sw.currCount = 0
		sw.currStart += sw.window
	} else {
		// Multiple windows elapsed: all history is stale
		sw.prevCount = 0
		sw.currCount = 0
		sw.currStart = now / sw.window * sw.window
	}
}

// estimate calculates the weighted event count across the sliding window.
// Uses linear interpolation between previous and current window.
func (sw *SlidingWindow) estimate() float64 {
	now := sw.now()
	elapsed := now - sw.currStart
	if elapsed <= 0 {
		return float64(sw.currCount)
	}

	// Weight: how much of the previous window is still relevant
	weight := 1.0 - (float64(elapsed) / float64(sw.window))
	if weight < 0 {
		weight = 0
	}

	return float64(sw.prevCount)*weight + float64(sw.currCount)
}
