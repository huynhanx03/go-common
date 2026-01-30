package timer

import (
	"sync"
	"sync/atomic"
	"time"
)

type Timer interface {
	Now() time.Time
	Stop()
}

type CachedTimer struct {
	now    atomic.Value
	step   time.Duration
	ticker *time.Ticker
	done   chan struct{}
	wg     sync.WaitGroup
}

func NewCachedTimer(step time.Duration) *CachedTimer {
	t := &CachedTimer{
		step:   step,
		ticker: time.NewTicker(step),
		done:   make(chan struct{}),
	}
	t.now.Store(time.Now())

	t.wg.Add(1)
	go t.run()

	return t
}

func (t *CachedTimer) run() {
	defer t.wg.Done()

	current := t.Now()

	for {
		select {
		case <-t.ticker.C:
			current = current.Add(t.step)
			t.now.Store(current)
		case <-t.done:
			t.ticker.Stop()
			return
		}
	}
}

func (t *CachedTimer) Now() time.Time {
	return t.now.Load().(time.Time)
}

func (t *CachedTimer) Stop() {
	close(t.done)
	t.wg.Wait()
}
