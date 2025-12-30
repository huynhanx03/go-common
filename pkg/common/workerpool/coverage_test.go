package workerpool

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWorker_Interface_Panics(t *testing.T) {
	// Test worker.inputParam panic
	w := &worker{}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("worker.inputParam should panic")
			}
		}()
		w.inputParam(1)
	}()

	// Test genericWorker.inputFunc panic
	gw := &genericWorker[int]{}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("genericWorker.inputFunc should panic")
			}
		}()
		gw.inputFunc(func() {})
	}()
}

func TestPool_Submit_Block_Then_Close(t *testing.T) {
	// Create a pool with 1 worker and max blocking 1
	p, err := NewPool(1, WithMaxBlockingTasks(1))
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}

	// Occupy the worker
	var wg sync.WaitGroup
	wg.Add(1)
	p.Submit(func() {
		// Run long enough to ensure the next submit blocks
		time.Sleep(200 * time.Millisecond)
		wg.Done()
	})

	// Start a goroutine that blocks on Submit
	errCh := make(chan error, 1)
	go func() {
		// This should block because worker is busy
		err := p.Submit(demoFunc)
		errCh <- err
	}()

	// Ensure the second submit is blocked
	time.Sleep(50 * time.Millisecond)

	// Release the pool. This should broadcast to unblock the waiting submit.
	p.Release()

	// Check the error from the blocked submit
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrPoolClosed) {
			t.Errorf("expected ErrPoolClosed, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for blocked submit to return")
	}

	wg.Wait()
}
