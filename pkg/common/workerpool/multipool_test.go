package workerpool

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMultiPool_Submit(t *testing.T) {
	size := 20
	sizePerPool := 10
	mp, err := NewMultiPool(size, sizePerPool, RoundRobin)
	if err != nil {
		t.Fatalf("create multipool failed: %v", err)
	}
	defer mp.Release()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		err := mp.Submit(func() {
			demoFunc()
			wg.Done()
		})
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}
	wg.Wait()

	if expected := size * sizePerPool; mp.Cap() != expected {
		t.Errorf("expected cap %d, got %d", expected, mp.Cap())
	}
}

func TestMultiPool_RoundRobin(t *testing.T) {
	size := 5
	sizePerPool := 1
	mp, err := NewMultiPool(size, sizePerPool, RoundRobin)
	if err != nil {
		t.Fatalf("create multipool failed: %v", err)
	}
	defer mp.Release()

	initialIndex := atomic.LoadUint32(&mp.index)
	_ = mp.Submit(demoFunc)
	afterIndex := atomic.LoadUint32(&mp.index)

	if initialIndex == math.MaxUint32 {
		if afterIndex != 0 {
			t.Errorf("expected index 0 after MaxUint32, got %d", afterIndex)
		}
	} else {
		if afterIndex != initialIndex+1 {
			t.Errorf("expected index %d, got %d", initialIndex+1, afterIndex)
		}
	}
}

func TestMultiPool_LeastTasks(t *testing.T) {
	size := 2
	sizePerPool := 10
	mp, err := NewMultiPool(size, sizePerPool, LeastTasks)
	if err != nil {
		t.Fatalf("create multipool failed: %v", err)
	}
	defer mp.Release()

	var wg sync.WaitGroup
	wg.Add(1)

	mp.Submit(func() {
		time.Sleep(100 * time.Millisecond)
		wg.Done()
	})

	if err := mp.Submit(demoFunc); err != nil {
		t.Errorf("submit failed: %v", err)
	}

	wg.Wait()
}

func TestMultiPool_ReleaseTimeout(t *testing.T) {
	mp, err := NewMultiPool(10, 10, RoundRobin)
	if err != nil {
		t.Fatalf("create multipool failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	mp.Submit(func() {
		time.Sleep(50 * time.Millisecond)
		wg.Done()
	})

	err = mp.ReleaseTimeout(1 * time.Second)
	if err != nil {
		t.Errorf("release timeout failed: %v", err)
	}
	wg.Wait()

	err = mp.Submit(demoFunc)
	if !errors.Is(err, ErrPoolClosed) {
		t.Errorf("expected ErrPoolClosed, got %v", err)
	}
}

func TestMultiPool_Reboot(t *testing.T) {
	mp, err := NewMultiPool(5, 5, RoundRobin)
	if err != nil {
		t.Fatalf("create multipool failed: %v", err)
	}

	mp.Release()
	if err := mp.Submit(demoFunc); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("expected ErrPoolClosed after release, got %v", err)
	}

	mp.Reboot()
	if err := mp.Submit(demoFunc); err != nil {
		t.Errorf("submit failed after reboot: %v", err)
	}
	mp.Release()
}

func TestMultiPool_Tune(t *testing.T) {
	numPools := 2
	sizePerPool := 10
	mp, err := NewMultiPool(numPools, sizePerPool, RoundRobin)
	if err != nil {
		t.Fatalf("create multipool failed: %v", err)
	}
	defer mp.Release()

	if mp.Cap() != numPools*sizePerPool {
		t.Errorf("initial cap mismatch, got %d", mp.Cap())
	}

	newSize := 20
	mp.Tune(newSize)

	if mp.Cap() != numPools*newSize {
		t.Errorf("cap after tune mismatch, got %d", mp.Cap())
	}
}

func TestMultiPool_Stats(t *testing.T) {
	numPools := 2
	sizePerPool := 1
	mp, err := NewMultiPool(numPools, sizePerPool, RoundRobin)
	if err != nil {
		t.Fatalf("create multipool failed: %v", err)
	}
	defer mp.Release()

	if mp.Free() != 2 {
		t.Errorf("expected 2 free workers, got %d", mp.Free())
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Occupy all workers
	_ = mp.Submit(func() { time.Sleep(50 * time.Millisecond); wg.Done() })
	_ = mp.Submit(func() { time.Sleep(50 * time.Millisecond); wg.Done() })

	// Wait a bit for them to start
	time.Sleep(10 * time.Millisecond)

	if mp.Running() != 2 {
		t.Errorf("expected 2 running workers, got %d", mp.Running())
	}
	if mp.Free() != 0 {
		t.Errorf("expected 0 free workers, got %d", mp.Free())
	}

	// Checking Waiting count is hard without blocking behavior, leading to overload.
	// But we can check it returns 0 at least.
	if mp.Waiting() != 0 {
		t.Errorf("expected 0 waiting, got %d", mp.Waiting())
	}

	wg.Wait()
}

func TestNewMultiPool_Error(t *testing.T) {
	_, err := NewMultiPool(10, 0, RoundRobin)
	if err == nil {
		t.Error("expected error with invalid size 0 for pool")
	}
}
