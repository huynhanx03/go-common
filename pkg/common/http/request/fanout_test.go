package request

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFanoutIsBoundedAndOrdered(t *testing.T) {
	t.Parallel()

	var active atomic.Int32
	var maximum atomic.Int32
	tasks := make([]func(context.Context) (int, error), 20)
	for index := range tasks {
		index := index
		tasks[index] = func(context.Context) (int, error) {
			current := active.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			return index, nil
		}
	}

	results, err := Fanout(context.Background(), FanoutOptions{
		MaxConcurrency: 3,
		Mode:           FanoutAll,
	}, tasks...)
	if err != nil {
		t.Fatalf("Fanout: %v", err)
	}
	if maximum.Load() > 3 {
		t.Fatalf("max concurrency = %d, want <= 3", maximum.Load())
	}
	for index, result := range results {
		if !result.Started || result.Err != nil || result.Value != index {
			t.Fatalf("result[%d] = %+v", index, result)
		}
	}
}

func TestFanoutContainsPanics(t *testing.T) {
	t.Parallel()

	tasks := []func(context.Context) (int, error){
		func(context.Context) (int, error) { return 1, nil },
		func(context.Context) (int, error) { panic("token=secret") },
	}
	results, err := Fanout(context.Background(), FanoutOptions{
		MaxConcurrency: 2,
		Mode:           FanoutAll,
	}, tasks...)
	if err != nil {
		t.Fatalf("Fanout all: %v", err)
	}
	if !errors.Is(results[1].Err, ErrTaskPanic) {
		t.Fatalf("panic result = %+v", results[1])
	}
	if results[1].Err.Error() != ErrTaskPanic.Error() {
		t.Fatalf("panic value leaked through error: %v", results[1].Err)
	}
}

func TestFanoutFirstErrorCancelsSiblings(t *testing.T) {
	t.Parallel()

	failure := errors.New("failed")
	started := make(chan struct{}, 3)
	tasks := []func(context.Context) (int, error){
		func(context.Context) (int, error) {
			started <- struct{}{}
			for len(started) < 3 {
				time.Sleep(time.Millisecond)
			}
			return 0, failure
		},
		func(ctx context.Context) (int, error) {
			started <- struct{}{}
			<-ctx.Done()
			return 0, ctx.Err()
		},
		func(ctx context.Context) (int, error) {
			started <- struct{}{}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	results, err := Fanout(context.Background(), FanoutOptions{
		MaxConcurrency: 3,
		Mode:           FanoutFirstError,
	}, tasks...)
	if !errors.Is(err, failure) {
		t.Fatalf("Fanout error = %v, want first failure", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
}

func TestFanoutValidatesOptions(t *testing.T) {
	t.Parallel()

	task := func(context.Context) (int, error) { return 1, nil }
	for _, options := range []FanoutOptions{
		{},
		{MaxConcurrency: -1, Mode: FanoutAll},
		{MaxConcurrency: 1, Mode: FanoutMode(99)},
	} {
		if _, err := Fanout(context.Background(), options, task); !errors.Is(err, ErrInvalidFanout) {
			t.Fatalf("Fanout(%+v) error = %v", options, err)
		}
	}
}
