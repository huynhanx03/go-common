package workerpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolSubmit(t *testing.T) {
	p, err := NewPool(4)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer p.Release()

	var n atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		if err := p.Submit(func() {
			n.Add(1)
			wg.Done()
		}); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	wg.Wait()

	if got := n.Load(); got != 100 {
		t.Fatalf("ran %d tasks, want 100", got)
	}
	if p.Cap() != 4 {
		t.Fatalf("Cap() = %d, want 4", p.Cap())
	}
}

func TestGenericPoolInvoke(t *testing.T) {
	var sum atomic.Int64
	var wg sync.WaitGroup

	p, err := NewGenericPool(4, func(v int) {
		sum.Add(int64(v))
		wg.Done()
	})
	if err != nil {
		t.Fatalf("NewGenericPool: %v", err)
	}
	defer p.Release()

	for i := 1; i <= 10; i++ {
		wg.Add(1)
		if err := p.Invoke(i); err != nil {
			t.Fatalf("Invoke(%d): %v", i, err)
		}
	}
	wg.Wait()

	if got := sum.Load(); got != 55 {
		t.Fatalf("sum = %d, want 55", got)
	}
}

func TestGenericPoolRequiresFunc(t *testing.T) {
	if _, err := NewGenericPool[int](4, nil); !errors.Is(err, ErrLackPoolFunc) {
		t.Fatalf("err = %v, want ErrLackPoolFunc", err)
	}
}

func TestPoolClosed(t *testing.T) {
	p, err := NewPool(1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	p.Release()

	if err := p.Submit(func() {}); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("err = %v, want ErrPoolClosed", err)
	}
}

func TestPoolNonblockingOverload(t *testing.T) {
	p, err := NewPool(1, WithNonblocking(true))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer p.Release()

	block := make(chan struct{})
	defer close(block)
	if err := p.Submit(func() { <-block }); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// The single worker is blocked; a nonblocking pool must reject the next task.
	if err := p.Submit(func() {}); !errors.Is(err, ErrPoolOverload) {
		t.Fatalf("err = %v, want ErrPoolOverload", err)
	}
}

func TestMultiPool(t *testing.T) {
	var n atomic.Int64
	var wg sync.WaitGroup

	p, err := NewGenericMultiPool(2, 2, func(v int) {
		n.Add(int64(v))
		wg.Done()
	}, RoundRobin)
	if err != nil {
		t.Fatalf("NewGenericMultiPool: %v", err)
	}
	defer func() {
		if err := p.ReleaseTimeout(time.Second); err != nil {
			t.Errorf("ReleaseTimeout: %v", err)
		}
	}()

	for i := 0; i < 20; i++ {
		wg.Add(1)
		if err := p.Invoke(1); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
	}
	wg.Wait()

	if got := n.Load(); got != 20 {
		t.Fatalf("ran %d tasks, want 20", got)
	}
}

func TestDefaultPoolSubmit(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	if err := Submit(func() { wg.Done() }); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	wg.Wait()

	if Running() < 0 || Cap() <= 0 {
		t.Fatalf("Running() = %d, Cap() = %d", Running(), Cap())
	}
	if Free() < 0 || Free() > Cap() {
		t.Fatalf("Free() = %d, Cap() = %d", Free(), Cap())
	}
}

func TestDefaultPoolCanReleaseAndReboot(t *testing.T) {
	Reboot()
	var started sync.WaitGroup
	started.Add(1)
	if err := Submit(func() { started.Done() }); err != nil {
		t.Fatalf("Submit before release: %v", err)
	}
	started.Wait()
	if err := ReleaseTimeout(time.Second); err != nil {
		t.Fatalf("ReleaseTimeout: %v", err)
	}
	if err := Submit(func() {}); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("Submit after release = %v, want closed pool", err)
	}

	Reboot()
	var rebooted sync.WaitGroup
	rebooted.Add(1)
	if err := Submit(func() { rebooted.Done() }); err != nil {
		t.Fatalf("Submit after reboot: %v", err)
	}
	rebooted.Wait()
	Release()
	if err := Submit(func() {}); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("Submit after Release = %v, want closed pool", err)
	}
	Reboot()
	if err := ReleaseContext(context.Background()); err != nil {
		t.Fatalf("ReleaseContext: %v", err)
	}
	Reboot()
}

func TestPoolFuncInvokesHandlerAndValidatesBoundedOperations(t *testing.T) {
	values := make(chan any, 1)
	pool, err := NewPoolFunc(1, func(value any) { values <- value })
	if err != nil {
		t.Fatalf("NewPoolFunc: %v", err)
	}
	if err := pool.Invoke("job-1"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	select {
	case got := <-values:
		if got != "job-1" {
			t.Fatalf("handler value = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not invoked")
	}
	if err := pool.ReleaseTimeout(time.Second); err != nil {
		t.Fatalf("ReleaseTimeout: %v", err)
	}
	if err := pool.InvokeContext(context.Background(), "after-release"); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("Invoke after release = %v, want closed pool", err)
	}
	if _, err := NewPoolFunc(1, nil); !errors.Is(err, ErrLackPoolFunc) {
		t.Fatalf("nil handler error = %v, want ErrLackPoolFunc", err)
	}
}

func TestPoolReleaseMethodsRejectInvalidBounds(t *testing.T) {
	pool, err := NewPool(1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if err := pool.ReleaseTimeout(0); !errors.Is(err, ErrInvalidPoolOptions) {
		t.Fatalf("ReleaseTimeout(0) = %v, want invalid options", err)
	}
	if err := pool.ReleaseContext(nil); !errors.Is(err, ErrInvalidPoolOptions) {
		t.Fatalf("ReleaseContext(nil) = %v, want invalid options", err)
	}
	pool.Release()
	if err := pool.SubmitContext(nil, func() {}); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("SubmitContext(nil) = %v, want invalid task", err)
	}
}

func TestPoolReleaseMethodsDrainEachPoolType(t *testing.T) {
	pool, err := NewPool(1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if err := pool.ReleaseContext(context.Background()); err != nil {
		t.Fatalf("Pool.ReleaseContext: %v", err)
	}

	functionPool, err := NewPoolFunc(1, func(any) {})
	if err != nil {
		t.Fatalf("NewPoolFunc: %v", err)
	}
	if err := functionPool.ReleaseContext(context.Background()); err != nil {
		t.Fatalf("PoolFunc.ReleaseContext: %v", err)
	}

	genericPool, err := NewGenericPool(1, func(int) {})
	if err != nil {
		t.Fatalf("NewGenericPool: %v", err)
	}
	if err := genericPool.ReleaseTimeout(time.Second); err != nil {
		t.Fatalf("GenericPool.ReleaseTimeout: %v", err)
	}
}

func TestMultiPoolAndMultiPoolFuncDispatch(t *testing.T) {
	var count atomic.Int64
	var wait sync.WaitGroup
	pool, err := NewMultiPool(2, 1, RoundRobin)
	if err != nil {
		t.Fatalf("NewMultiPool: %v", err)
	}
	for range 8 {
		wait.Add(1)
		if err := pool.Submit(func() { count.Add(1); wait.Done() }); err != nil {
			t.Fatalf("MultiPool.Submit: %v", err)
		}
	}
	wait.Wait()
	if err := pool.ReleaseTimeout(time.Second); err != nil {
		t.Fatalf("MultiPool.ReleaseTimeout: %v", err)
	}
	if got := count.Load(); got != 8 {
		t.Fatalf("MultiPool completed %d tasks, want 8", got)
	}

	values := make(chan any, 1)
	functionPool, err := NewMultiPoolFunc(2, 1, func(value any) { values <- value }, RoundRobin)
	if err != nil {
		t.Fatalf("NewMultiPoolFunc: %v", err)
	}
	if err := functionPool.Invoke("payload"); err != nil {
		t.Fatalf("MultiPoolFunc.Invoke: %v", err)
	}
	select {
	case got := <-values:
		if got != "payload" {
			t.Fatalf("MultiPoolFunc value = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("MultiPoolFunc did not dispatch payload")
	}
	if err := functionPool.ReleaseTimeout(time.Second); err != nil {
		t.Fatalf("MultiPoolFunc.ReleaseTimeout: %v", err)
	}
}
