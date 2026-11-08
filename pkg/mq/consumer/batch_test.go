package consumer_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func TestCollectReadsBoundedWindowsAndFlushesAvailableItems(t *testing.T) {
	t.Run("already closed source has no work", func(t *testing.T) {
		source := make(chan int)
		close(source)
		got, err := consumer.Collect(context.Background(), source, 3, time.Second)
		if err != nil || got != nil {
			t.Fatalf("Collect() = (%v, %v), want no work", got, err)
		}
	})

	t.Run("fills at the configured size and leaves later messages queued", func(t *testing.T) {
		source := make(chan int, 4)
		for _, value := range []int{1, 2, 3, 4} {
			source <- value
		}
		got, err := consumer.Collect(context.Background(), source, 3, time.Hour)
		if err != nil || !reflect.DeepEqual(got, []int{1, 2, 3}) {
			t.Fatalf("Collect() = (%v, %v)", got, err)
		}
		if got := <-source; got != 4 {
			t.Fatalf("next source item = %d, want 4", got)
		}
	})

	t.Run("zero wait drains only immediately available messages", func(t *testing.T) {
		source := make(chan string, 2)
		source <- "one"
		source <- "two"
		got, err := consumer.Collect(context.Background(), source, 10, 0)
		if err != nil || !reflect.DeepEqual(got, []string{"one", "two"}) {
			t.Fatalf("Collect() = (%v, %v)", got, err)
		}
	})

	t.Run("zero wait flushes when source closes", func(t *testing.T) {
		source := make(chan int, 1)
		source <- 5
		close(source)
		got, err := consumer.Collect(context.Background(), source, 5, 0)
		if err != nil || !reflect.DeepEqual(got, []int{5}) {
			t.Fatalf("Collect() = (%v, %v)", got, err)
		}
	})

	t.Run("closed source flushes a partial window", func(t *testing.T) {
		source := make(chan int, 2)
		source <- 7
		source <- 8
		close(source)
		got, err := consumer.Collect(context.Background(), source, 5, time.Hour)
		if err != nil || !reflect.DeepEqual(got, []int{7, 8}) {
			t.Fatalf("Collect() = (%v, %v)", got, err)
		}
	})

	t.Run("wait expiry flushes a partial window", func(t *testing.T) {
		source := make(chan int, 1)
		source <- 9
		got, err := consumer.Collect(context.Background(), source, 5, 10*time.Millisecond)
		if err != nil || !reflect.DeepEqual(got, []int{9}) {
			t.Fatalf("Collect() = (%v, %v)", got, err)
		}
	})
}

func TestCollectDoesNotReturnAWindowAfterCancellation(t *testing.T) {
	t.Run("canceled before first item", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := consumer.Collect(ctx, make(chan int), 2, time.Second)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("Collect() = (%v, %v), want canceled with no window", got, err)
		}
	})

	t.Run("canceled while gathering", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		source := make(chan int, 1)
		source <- 1
		done := make(chan struct {
			values []int
			err    error
		}, 1)
		go func() {
			values, err := consumer.Collect(ctx, source, 3, time.Hour)
			done <- struct {
				values []int
				err    error
			}{values, err}
		}()
		cancel()
		result := <-done
		if !errors.Is(result.err, context.Canceled) || result.values != nil {
			t.Fatalf("Collect() = (%v, %v), want canceled with no partial window", result.values, result.err)
		}
	})

	t.Run("canceled timed gather returns no partial window", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &cancelOnSecondDone{Context: base, cancel: cancel}
		source := make(chan int, 1)
		source <- 3
		got, err := consumer.Collect(ctx, source, 2, time.Hour)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("Collect() = (%v, %v), want canceled with no partial window", got, err)
		}
	})

	t.Run("zero wait observes cancellation after taking the first message", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &cancelOnSecondDone{Context: base, cancel: cancel}
		source := make(chan int, 1)
		source <- 2
		got, err := consumer.Collect(ctx, source, 3, 0)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("Collect() = (%v, %v), want canceled with no partial window", got, err)
		}
	})
}

func TestCollectRejectsInvalidWindow(t *testing.T) {
	for _, test := range []struct {
		name string
		size int
		wait time.Duration
	}{
		{name: "zero size", size: 0},
		{name: "negative size", size: -1},
		{name: "negative wait", size: 1, wait: -time.Nanosecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := consumer.Collect(context.Background(), make(chan int), test.size, test.wait); !errors.Is(err, consumer.ErrInvalidConfig) {
				t.Fatalf("Collect() error = %v", err)
			}
		})
	}
}

type cancelOnSecondDone struct {
	context.Context
	calls  int
	cancel context.CancelFunc
}

func (ctx *cancelOnSecondDone) Done() <-chan struct{} {
	ctx.calls++
	if ctx.calls == 2 {
		ctx.cancel()
	}
	return ctx.Context.Done()
}
