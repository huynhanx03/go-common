package workerpool

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenericMultiPool_Invoke(t *testing.T) {
	size := 10
	sizePerPool := 10
	var wg sync.WaitGroup

	mp, err := NewGenericMultiPool(size, sizePerPool, func(i int) {
		demoPoolFuncInt(i)
		wg.Done()
	}, RoundRobin)
	if err != nil {
		t.Fatalf("create generic multipool failed: %v", err)
	}
	defer mp.Release()

	for i := 0; i < n; i++ {
		wg.Add(1)
		err := mp.Invoke(Param)
		if err != nil {
			t.Fatalf("invoke failed: %v", err)
		}
	}
	wg.Wait()
}

func TestGenericMultiPool_RoundRobin(t *testing.T) {
	size := 5
	sizePerPool := 1
	mp, err := NewGenericMultiPool(size, sizePerPool, func(i int) {}, RoundRobin)
	if err != nil {
		t.Fatalf("create generic multipool failed: %v", err)
	}
	defer mp.Release()

	initialIndex := atomic.LoadUint32(&mp.index)
	_ = mp.Invoke(1)
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

func TestGenericMultiPool_LeastTasks(t *testing.T) {
	size := 2
	sizePerPool := 10
	mp, err := NewGenericMultiPool(size, sizePerPool, func(d time.Duration) {
		time.Sleep(d)
	}, LeastTasks)
	if err != nil {
		t.Fatalf("create generic multipool failed: %v", err)
	}
	defer mp.Release()

	// Create load
	var wg sync.WaitGroup
	wg.Add(1)
	_ = mp.Invoke(100 * time.Millisecond) // This will occupy one worker in a pool

	// Invoke another quick task
	if err := mp.Invoke(0); err != nil {
		t.Errorf("invoke failed: %v", err)
	}
}

func TestGenericMultiPool_Lifecycle(t *testing.T) {
	mp, err := NewGenericMultiPool(5, 5, func(i int) {}, RoundRobin)
	if err != nil {
		t.Fatalf("create generic multipool failed: %v", err)
	}

	// Test Release
	mp.Release()
	if err := mp.Invoke(1); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("expected ErrPoolClosed after release, got %v", err)
	}

	// Test Reboot
	mp.Reboot()
	if err := mp.Invoke(1); err != nil {
		t.Errorf("invoke failed after reboot: %v", err)
	}

	// Test ReleaseTimeout
	if err := mp.ReleaseTimeout(time.Second); err != nil {
		t.Errorf("release timeout failed: %v", err)
	}
	if err := mp.Invoke(1); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("expected ErrPoolClosed after release timeout, got %v", err)
	}
}
