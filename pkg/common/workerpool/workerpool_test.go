package workerpool

import (
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
}
