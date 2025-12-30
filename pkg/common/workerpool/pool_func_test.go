package workerpool

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenericPool_Invoke(t *testing.T) {
	var wg sync.WaitGroup
	p, err := NewGenericPool(PoolSize, func(i int) {
		demoPoolFuncInt(i)
		wg.Done()
	})
	if err != nil {
		t.Fatalf("create generic pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < n; i++ {
		wg.Add(1)
		err := p.Invoke(Param)
		if err != nil {
			t.Fatalf("invoke failed: %v", err)
		}
	}
	wg.Wait()
	t.Logf("pool with func, running workers number:%d", p.Running())
}

func TestGenericPool_Invoke_PreAlloc(t *testing.T) {
	var wg sync.WaitGroup
	p, err := NewGenericPool(PoolSize, func(i int) {
		demoPoolFuncInt(i)
		wg.Done()
	}, WithPreAlloc(true))
	if err != nil {
		t.Fatalf("create generic pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < n; i++ {
		wg.Add(1)
		err := p.Invoke(Param)
		if err != nil {
			t.Fatalf("invoke failed: %v", err)
		}
	}
	wg.Wait()
	t.Logf("pool with func, running workers number:%d", p.Running())
}

func TestGenericPool_Purge(t *testing.T) {
	size := 10
	ch := make(chan struct{})
	p, err := NewGenericPool(size, func(i int) {
		<-ch
	})
	if err != nil {
		t.Fatalf("create generic pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < size; i++ {
		_ = p.Invoke(i)
	}

	if n := p.Running(); n != size {
		t.Errorf("pool should be full, expected %d, got %d", size, n)
	}

	close(ch)
	time.Sleep(2 * DefaultCleanIntervalTime)

	if n := p.Running(); n != 0 {
		t.Errorf("pool should be empty after purge, got %d", n)
	}
}

func TestGenericPool_PanicHandler(t *testing.T) {
	var panicCounter int64
	var wg sync.WaitGroup
	p, err := NewGenericPool(10, func(i int) {
		panic("Oops!")
	}, WithPanicHandler(func(p any) {
		defer wg.Done()
		atomic.AddInt64(&panicCounter, 1)
		t.Logf("catch panic with PanicHandler: %v", p)
	}))
	if err != nil {
		t.Fatalf("create generic pool failed: %v", err)
	}
	defer p.Release()

	wg.Add(1)
	_ = p.Invoke(1)
	wg.Wait()

	if c := atomic.LoadInt64(&panicCounter); c != 1 {
		t.Errorf("panic handler count mismatch, expected 1, got %d", c)
	}
}

func TestGenericPool_Nonblocking(t *testing.T) {
	poolSize := 10
	p, err := NewGenericPool(poolSize, func(ch chan struct{}) {
		<-ch
	}, WithNonblocking(true))
	if err != nil {
		t.Fatalf("create generic pool failed: %v", err)
	}
	defer p.Release()

	ch := make(chan struct{})

	for i := 0; i < poolSize; i++ {
		if err := p.Invoke(ch); err != nil {
			t.Fatalf("safe invoke failed: %v", err)
		}
	}

	if err := p.Invoke(ch); !errors.Is(err, ErrPoolOverload) {
		t.Fatalf("expected ErrPoolOverload in nonblocking mode, got %v", err)
	}

	close(ch)
}

func TestGenericPool_Tune(t *testing.T) {
	p, err := NewGenericPool(10, func(i int) {})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if p.Cap() != 10 {
		t.Errorf("cap mismatch, got %d", p.Cap())
	}

	p.Tune(20)
	if p.Cap() != 20 {
		t.Errorf("tune failed, got %d", p.Cap())
	}
}
