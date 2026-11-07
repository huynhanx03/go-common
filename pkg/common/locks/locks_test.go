package locks_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

func TestSpinLockProtectsSharedUpdatesUnderContention(t *testing.T) {
	const workers, iterations = 32, 2_000
	lock := locks.NewSpinLock()
	var total int
	var group sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for range iterations {
				lock.Lock()
				total++
				lock.Unlock()
			}
		}()
	}
	close(start)
	group.Wait()
	if total != workers*iterations {
		t.Fatalf("protected updates = %d, want %d", total, workers*iterations)
	}
}

func TestRWSpinLockAllowsReadersAndExcludesWriters(t *testing.T) {
	lock := locks.NewRWSpinLock()
	lock.RLock()
	readerStarted := make(chan struct{})
	readerAcquired := make(chan struct{})
	go func() {
		close(readerStarted)
		lock.RLock()
		close(readerAcquired)
		lock.RUnlock()
	}()
	<-readerStarted
	select {
	case <-readerAcquired:
	case <-time.After(time.Second):
		lock.RUnlock()
		t.Fatal("a second reader blocked while a reader held the lock")
	}
	lock.RUnlock()

	const readers, writers, iterations = 8, 4, 500
	var activeReaders, activeWriters atomic.Int32
	var violation atomic.Bool
	var group sync.WaitGroup
	start := make(chan struct{})
	for range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for range iterations {
				lock.RLock()
				activeReaders.Add(1)
				if activeWriters.Load() != 0 {
					violation.Store(true)
				}
				activeReaders.Add(-1)
				lock.RUnlock()
			}
		}()
	}
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for range iterations {
				lock.Lock()
				activeWriters.Add(1)
				if activeReaders.Load() != 0 || activeWriters.Load() != 1 {
					violation.Store(true)
				}
				activeWriters.Add(-1)
				lock.Unlock()
			}
		}()
	}
	close(start)
	group.Wait()
	if violation.Load() {
		t.Fatal("readers overlapped a writer or multiple writers overlapped")
	}
}

func TestRWSpinLockRejectsUnmatchedUnlocks(t *testing.T) {
	for name, call := range map[string]func(){
		"read unlock":  func() { locks.NewRWSpinLock().RUnlock() },
		"write unlock": func() { locks.NewRWSpinLock().Unlock() },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("unmatched unlock did not panic")
				}
			}()
			call()
		})
	}
}
