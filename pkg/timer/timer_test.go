package timer

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewCachedTimerRejectsNonPositiveStep(t *testing.T) {
	t.Parallel()

	for _, step := range []time.Duration{0, -time.Nanosecond} {
		if _, err := NewCachedTimer(step); !errors.Is(err, ErrInvalidStep) {
			t.Fatalf("NewCachedTimer(%s) error = %v", step, err)
		}
	}
}

func TestCachedTimerStalenessAndConcurrentStopAreBounded(t *testing.T) {
	t.Parallel()

	step := 5 * time.Millisecond
	timer, err := NewCachedTimer(step)
	if err != nil {
		t.Fatalf("NewCachedTimer: %v", err)
	}
	time.Sleep(3 * step)
	now := time.Now()
	cached := time.Unix(0, timer.Now())
	if staleness := now.Sub(cached); staleness < 0 || staleness > 4*step {
		t.Fatalf("cached time staleness = %s", staleness)
	}

	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			timer.Stop()
		}()
	}
	wait.Wait()
	timer.Stop()
}
