package workerpool

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPool_Submit(t *testing.T) {
	var wg sync.WaitGroup
	p, err := NewPool(PoolSize)
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < n; i++ {
		wg.Add(1)
		err := p.Submit(func() {
			demoFunc()
			wg.Done()
		})
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}
	wg.Wait()
	t.Logf("pool, running workers number:%d", p.Running())
	// mem := runtime.MemStats{}
	// runtime.ReadMemStats(&mem)
	// curMem = mem.TotalAlloc/MiB - curMem
	// t.Logf("memory usage:%d MB", curMem)
}

func TestPool_Submit_PreAlloc(t *testing.T) {
	var wg sync.WaitGroup
	p, err := NewPool(PoolSize, WithPreAlloc(true))
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < n; i++ {
		wg.Add(1)
		err := p.Submit(func() {
			demoFunc()
			wg.Done()
		})
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}
	wg.Wait()
	t.Logf("pool, running workers number:%d", p.Running())
}

func TestPool_Purge(t *testing.T) {
	size := 500
	ch := make(chan struct{})

	p, err := NewPool(size)
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < size; i++ {
		j := i + 1
		_ = p.Submit(func() {
			<-ch
			d := j % 100
			time.Sleep(time.Duration(d) * time.Millisecond)
		})
	}

	if n := p.Running(); n != size {
		t.Errorf("pool should be full, expected: %d, but got: %d", size, n)
	}

	close(ch)
	time.Sleep(2 * DefaultCleanIntervalTime)
	if n := p.Running(); n != 0 {
		t.Errorf("pool should be empty after purge, but got %d", n)
	}
}

func TestPool_Nonblocking(t *testing.T) {
	poolSize := 10
	p, err := NewPool(poolSize, WithNonblocking(true))
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < poolSize-1; i++ {
		if err := p.Submit(longRunningFunc); err != nil {
			t.Fatalf("nonblocking submit when pool is not full shouldn't return error: %v", err)
		}
	}

	ch := make(chan struct{})
	ch1 := make(chan struct{})
	f := func() {
		<-ch
		close(ch1)
	}
	// p is full now.
	if err := p.Submit(f); err != nil {
		t.Fatalf("nonblocking submit when pool is not full shouldn't return error: %v", err)
	}

	if err := p.Submit(demoFunc); !errors.Is(err, ErrPoolOverload) {
		t.Fatalf("nonblocking submit when pool is full should get ErrPoolOverload, but got: %v", err)
	}

	close(ch)
	<-ch1
	if err := p.Submit(demoFunc); err != nil {
		t.Fatalf("nonblocking submit when pool available should not return error: %v", err)
	}
}

func TestPool_MaxBlocking(t *testing.T) {
	poolSize := 10
	p, err := NewPool(poolSize, WithMaxBlockingTasks(1))
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}
	defer p.Release()

	for i := 0; i < poolSize-1; i++ {
		if err := p.Submit(longRunningFunc); err != nil {
			t.Fatalf("submit when pool is not full shouldn't return error: %v", err)
		}
	}

	ch := make(chan struct{})
	f := func() {
		<-ch
	}
	// p is full now.
	if err := p.Submit(f); err != nil {
		t.Fatalf("submit when pool is not full shouldn't return error: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	errCh := make(chan error, 1)
	go func() {
		if err := p.Submit(demoFunc); err != nil {
			errCh <- err
		}
		wg.Done()
	}()

	time.Sleep(500 * time.Millisecond)
	// already reached max blocking limit
	if err := p.Submit(demoFunc); !errors.Is(err, ErrPoolOverload) {
		t.Fatalf("blocking submit when pool reach max blocking submit should return ErrPoolOverload, but got: %v", err)
	}

	close(ch)
	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatalf("blocking submit when pool is full should not return error, but got: %v", err)
	default:
	}
}

func TestPool_Reboot(t *testing.T) {
	var wg sync.WaitGroup
	p, err := NewPool(10)
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}
	defer p.Release()

	wg.Add(1)
	_ = p.Submit(func() {
		demoFunc()
		wg.Done()
	})
	wg.Wait()

	if err := p.ReleaseTimeout(time.Second); err != nil {
		t.Fatalf("release timeout failed: %v", err)
	}
	if err := p.Submit(nil); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("pool should be closed, but got error: %v", err)
	}

	p.Reboot()
	wg.Add(1)
	if err := p.Submit(func() { wg.Done() }); err != nil {
		t.Fatalf("pool should be rebooted, but got error: %v", err)
	}
	wg.Wait()
}

func TestPool_Infinite(t *testing.T) {
	c := make(chan struct{})
	p, err := NewPool(-1)
	if err != nil {
		t.Fatalf("create infinite pool failed: %v", err)
	}
	defer p.Release()

	_ = p.Submit(func() {
		_ = p.Submit(func() {
			<-c
		})
	})
	c <- struct{}{}

	// Give some time for workers to spin up
	time.Sleep(10 * time.Millisecond)

	if n := p.Running(); n != 2 {
		t.Errorf("expect 2 workers running, but got %d", n)
	}
	if n := p.Free(); n != -1 {
		t.Errorf("expect -1 of free workers by unlimited pool, but got %d", n)
	}
}

func TestPool_PanicHandler(t *testing.T) {
	var panicCounter int64
	var wg sync.WaitGroup
	p, err := NewPool(10, WithPanicHandler(func(p any) {
		defer wg.Done()
		atomic.AddInt64(&panicCounter, 1)
		t.Logf("catch panic with PanicHandler: %v", p)
	}))
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}
	defer p.Release()

	wg.Add(1)
	_ = p.Submit(func() {
		panic("Oops!")
	})
	wg.Wait()

	if c := atomic.LoadInt64(&panicCounter); c != 1 {
		t.Errorf("panic handler count mismatch, expected 1, got %d", c)
	}
}

func TestPool_Tune(t *testing.T) {
	p, err := NewPool(10)
	if err != nil {
		t.Fatalf("create pool failed: %v", err)
	}

	if p.Cap() != 10 {
		t.Errorf("expect capacity 10, got %d", p.Cap())
	}

	p.Tune(20)
	if p.Cap() != 20 {
		t.Errorf("expect capacity 20 after tune, got %d", p.Cap())
	}

	p.Tune(5)
	if p.Cap() != 5 {
		t.Errorf("expect capacity 5 after tune, got %d", p.Cap())
	}

	// Tune with same value or invalid value should be ignored
	p.Tune(5)
	if p.Cap() != 5 {
		t.Errorf("capacity changed unexpectedly")
	}
	p.Tune(-1)
	if p.Cap() != 5 {
		t.Errorf("capacity should not change with invalid negative size")
	}
}
