package workerpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolConstructorsRejectUnboundedAndInvalidConfiguration(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, -1, maxPoolSize + 1} {
		if _, err := NewPool(size); !errors.Is(err, ErrInvalidPoolSize) {
			t.Fatalf("NewPool(%d) error = %v", size, err)
		}
	}
	if _, err := NewPool(1, WithMaxBlockingTasks(-1)); !errors.Is(
		err,
		ErrInvalidPoolOptions,
	) {
		t.Fatalf("negative MaxBlockingTasks error = %v", err)
	}
	if _, err := NewPool(1, WithExpiryDuration(-time.Second)); !errors.Is(
		err,
		ErrInvalidPoolOptions,
	) {
		t.Fatalf("negative ExpiryDuration error = %v", err)
	}
}

func TestPoolSubmitContextCancelsBoundedAdmission(t *testing.T) {
	t.Parallel()

	pool, err := NewPool(1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Release()

	block := make(chan struct{})
	started := make(chan struct{})
	if err := pool.Submit(func() {
		close(started)
		<-block
	}); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var ran atomic.Bool
	if err := pool.SubmitContext(ctx, func() { ran.Store(true) }); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("SubmitContext error = %v", err)
	}
	close(block)
	time.Sleep(10 * time.Millisecond)
	if ran.Load() {
		t.Fatal("canceled task was admitted")
	}
}

func TestGenericPoolContainsPanicAndDrainsIdempotently(t *testing.T) {
	t.Parallel()

	panicSeen := make(chan struct{}, 1)
	var completed atomic.Int32
	pool, err := NewGenericPool(
		2,
		func(value int) {
			if value == 1 {
				panic("credential=secret")
			}
			completed.Add(1)
		},
		WithPanicHandler(func(any) {
			panicSeen <- struct{}{}
		}),
	)
	if err != nil {
		t.Fatalf("NewGenericPool: %v", err)
	}
	if err := pool.InvokeContext(context.Background(), 1); err != nil {
		t.Fatalf("panic InvokeContext: %v", err)
	}
	if err := pool.InvokeContext(context.Background(), 2); err != nil {
		t.Fatalf("second InvokeContext: %v", err)
	}
	select {
	case <-panicSeen:
	case <-time.After(time.Second):
		t.Fatal("panic handler was not called")
	}
	waitDeadline := time.Now().Add(time.Second)
	for completed.Load() != 1 && time.Now().Before(waitDeadline) {
		time.Sleep(time.Millisecond)
	}
	if completed.Load() != 1 {
		t.Fatal("pool did not continue after panic")
	}

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := pool.ReleaseContext(ctx); err != nil &&
				!errors.Is(err, ErrPoolClosed) {
				t.Errorf("ReleaseContext: %v", err)
			}
		}()
	}
	wait.Wait()
	if err := pool.InvokeContext(context.Background(), 3); !errors.Is(
		err,
		ErrPoolClosed,
	) {
		t.Fatalf("InvokeContext after release error = %v", err)
	}
}
