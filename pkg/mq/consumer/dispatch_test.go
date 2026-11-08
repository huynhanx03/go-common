package consumer_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func TestProcessPreservesStreamsAndStopsAfterRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	aStarted, bStarted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var calls []string
	failure := errors.New("temporarily unavailable")
	r, err := consumer.New(func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
		mu.Lock()
		calls = append(calls, message.ID)
		mu.Unlock()
		switch message.ID {
		case "a0":
			close(aStarted)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return consumer.Retry, failure
		case "b0":
			close(bStarted)
		}
		return consumer.Success, nil
	}, consumer.WithWorkers(3))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	done := make(chan []consumer.Result, 1)
	go func() {
		done <- r.Process(ctx, []consumer.Message{
			{ID: "a0", Stream: "a"}, {ID: "a1", Stream: "a"},
			{ID: "b0", Stream: "b"}, {ID: "b1", Stream: "b"},
		})
	}()
	for _, started := range []chan struct{}{aStarted, bStarted} {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("independent streams did not run concurrently")
		}
	}
	close(release)
	results := <-done
	if !errors.Is(results[0].Err, failure) || results[1].Outcome != consumer.Retry ||
		results[2].Outcome != consumer.Success || results[3].Outcome != consumer.Success {
		t.Fatalf("unexpected results: %+v", results)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, call := range calls {
		if call == "a1" {
			t.Fatal("executed a message after its stream requested retry")
		}
	}
}

func TestProcessBackpressure(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "block", true: "reject"}[reject], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			opts := []consumer.Option{consumer.WithQueueCapacity(1), consumer.WithUnordered()}
			if reject {
				opts = append(opts, consumer.WithFullQueue(consumer.Reject))
			}
			r, err := consumer.New(func(_ context.Context, message consumer.Message) (consumer.Outcome, error) {
				if message.ID == "active" {
					close(started)
					<-release
				}
				return consumer.Success, nil
			}, opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(ctx)
			active := make(chan []consumer.Result, 1)
			go func() { active <- r.Process(ctx, []consumer.Message{{ID: "active"}}) }()
			<-started
			queuedCtx, queuedCancel := context.WithCancel(ctx)
			queued := make(chan []consumer.Result, 1)
			go func() { queued <- r.Process(queuedCtx, []consumer.Message{{ID: "queued"}}) }()
			waitQueued(t, ctx, r, 1)
			fullCtx, fullCancel := context.WithTimeout(ctx, 25*time.Millisecond)
			results := r.Process(fullCtx, []consumer.Message{{ID: "overflow"}})
			fullCancel()
			want := error(context.DeadlineExceeded)
			if reject {
				want = consumer.ErrQueueFull
			}
			if !errors.Is(results[0].Err, want) {
				t.Fatalf("overflow: %+v, want %v", results[0], want)
			}
			queuedCancel()
			if result := <-queued; !errors.Is(result[0].Err, context.Canceled) {
				t.Fatalf("queued cancellation: %+v", result)
			}
			close(release)
			if result := <-active; result[0].Outcome != consumer.Success {
				t.Fatalf("active: %+v", result)
			}
		})
	}
}

func TestQueueRejectionPreservesEarlierAcceptedMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release, admitted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r, err := consumer.New(func(_ context.Context, m consumer.Message) (consumer.Outcome, error) {
		if m.ID == "active" {
			close(started)
			<-release
		}
		return consumer.Success, nil
	}, consumer.WithQueueCapacity(1), consumer.WithFullQueue(consumer.Reject), consumer.WithPriority(func(m consumer.Message) int {
		if m.ID == "after" {
			close(admitted)
		}
		return 0
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	active := make(chan []consumer.Result, 1)
	go func() { active <- r.Process(ctx, []consumer.Message{{ID: "active"}}) }()
	<-started
	done := make(chan []consumer.Result, 1)
	go func() { done <- r.Process(ctx, []consumer.Message{{ID: "accepted"}, {ID: "rejected"}, {ID: "after"}}) }()
	select {
	case <-admitted:
	case <-ctx.Done():
		t.Fatal("admission stalled")
	}
	close(release)
	<-active
	results := <-done
	if results[0].Outcome != consumer.Success || !errors.Is(results[1].Err, consumer.ErrQueueFull) {
		t.Fatalf("results %+v", results)
	}
}

func TestCancellationDoesNotExposeCallerBuffersToRunningHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started, read := make(chan struct{}), make(chan struct{})
	seen := make(chan consumer.Message, 1)
	r, err := consumer.New(func(_ context.Context, m consumer.Message) (consumer.Outcome, error) {
		close(started)
		<-read
		seen <- m
		return consumer.Success, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	message := consumer.Message{Key: []byte("key"), Value: []byte("original"), Headers: []consumer.Header{{Key: "custom", Value: []byte("header")}}}
	done := make(chan []consumer.Result, 1)
	go func() { done <- r.Process(ctx, []consumer.Message{message}) }()
	<-started
	cancel()
	if results := <-done; !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("result %+v", results)
	}
	message.Key[0] = 'X'
	message.Value[0] = 'X'
	message.Headers[0].Value[0] = 'X'
	message.Headers[0].Key = "changed"
	close(read)
	if got := <-seen; string(got.Key) != "key" || string(got.Value) != "original" || got.Headers[0].Key != "custom" || string(got.Headers[0].Value) != "header" {
		t.Fatalf("retained caller storage: %+v", got)
	}
}

func TestInvalidOptionsFailBeforeStartingWork(t *testing.T) {
	if _, err := consumer.New(nil); !errors.Is(err, consumer.ErrInvalidConfig) {
		t.Fatalf("New without a handler: %v", err)
	}
	if _, err := consumer.NewBatch(nil); !errors.Is(err, consumer.ErrInvalidConfig) {
		t.Fatalf("NewBatch without a handler: %v", err)
	}
	for _, options := range [][]consumer.Option{
		{nil}, {consumer.WithWorkers(0)}, {consumer.WithQueueCapacity(0)},
		{consumer.WithFullQueue(consumer.FullQueue(99))}, {consumer.WithPriorityBurst(0)},
		{consumer.WithBatch(0, 0)}, {consumer.WithBatch(2, -time.Second)},
	} {
		_, err := consumer.NewBatch(func(context.Context, []consumer.Message) []consumer.Result {
			t.Error("invalid runtime started handler")
			return nil
		}, options...)
		if !errors.Is(err, consumer.ErrInvalidConfig) {
			t.Fatalf("invalid configuration accepted: %v", err)
		}
	}
}

func TestCanceledFollowerReleasesCapacityWithoutBypassingActivePredecessor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	r, err := consumer.New(func(_ context.Context, m consumer.Message) (consumer.Outcome, error) {
		if m.ID == "active" {
			close(started)
			<-release
		}
		return consumer.Success, nil
	}, consumer.WithWorkers(2), consumer.WithQueueCapacity(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(release); _ = r.Close(context.Background()) }()
	groupCtx, stop := context.WithCancel(ctx)
	done := make(chan []consumer.Result, 1)
	go func() {
		done <- r.Process(groupCtx, []consumer.Message{{ID: "active", Stream: "a"}, {ID: "follower", Stream: "a"}})
	}()
	<-started
	waitQueued(t, ctx, r, 1)
	stop()
	<-done
	independentCtx, independentCancel := context.WithTimeout(ctx, time.Second)
	defer independentCancel()
	if results := r.Process(independentCtx, []consumer.Message{{ID: "independent", Stream: "b"}}); results[0].Outcome != consumer.Success {
		t.Fatalf("canceled follower blocked independent stream: %+v", results)
	}
	orderedCtx, orderedCancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer orderedCancel()
	if results := r.Process(orderedCtx, []consumer.Message{{ID: "must-wait", Stream: "a"}}); !errors.Is(results[0].Err, context.DeadlineExceeded) {
		t.Fatalf("bypassed running predecessor: %+v", results)
	}
}

func TestBatchFlushesForBackpressureBeforeItsGatheringDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := consumer.NewBatch(func(_ context.Context, messages []consumer.Message) []consumer.Result {
		results := make([]consumer.Result, len(messages))
		for i := range results {
			results[i].Outcome = consumer.Success
		}
		return results
	}, consumer.WithQueueCapacity(1), consumer.WithBatch(10, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	results := r.Process(ctx, []consumer.Message{{ID: "one"}, {ID: "two"}, {ID: "three"}})
	for _, result := range results {
		if result.Outcome != consumer.Success {
			t.Fatalf("batch waited despite blocking its producer: %+v", results)
		}
	}
}

func TestPriorityMakesProgressForOldestReadyMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var calls []string
	r, err := consumer.New(func(_ context.Context, message consumer.Message) (consumer.Outcome, error) {
		if message.ID == "active" {
			close(started)
			<-release
		} else {
			calls = append(calls, message.ID)
		}
		return consumer.Success, nil
	}, consumer.WithUnordered(), consumer.WithPriority(func(m consumer.Message) int {
		if m.ID == "low" {
			return 0
		}
		return 10
	}), consumer.WithPriorityBurst(2))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	active := make(chan []consumer.Result, 1)
	go func() { active <- r.Process(ctx, []consumer.Message{{ID: "active"}}) }()
	<-started
	done := make(chan []consumer.Result, 1)
	go func() {
		done <- r.Process(ctx, []consumer.Message{{ID: "low"}, {ID: "high1"}, {ID: "high2"}, {ID: "high3"}})
	}()
	waitQueued(t, ctx, r, 4)
	close(release)
	<-active
	<-done
	if want := []string{"high1", "high2", "low", "high3"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("execution order %v, want %v", calls, want)
	}
}

func TestBatchReturnsPartialResultsAndPreservesOrdering(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failure := errors.New("provider unavailable")
	t.Run("partial results", func(t *testing.T) {
		var sizes []int
		r, err := consumer.NewBatch(func(_ context.Context, batch []consumer.Message) []consumer.Result {
			sizes = append(sizes, len(batch))
			return []consumer.Result{{Outcome: consumer.Success}, {Outcome: consumer.Retry, Err: failure}, {Outcome: consumer.DeadLetter, Err: failure}}
		}, consumer.WithUnordered(), consumer.WithBatch(3, 10*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close(ctx)
		results := r.Process(ctx, []consumer.Message{{ID: "one"}, {ID: "two"}, {ID: "three"}})
		if !reflect.DeepEqual(sizes, []int{3}) || results[0].Outcome != consumer.Success ||
			!errors.Is(results[1].Err, failure) || results[2].Outcome != consumer.DeadLetter {
			t.Fatalf("batches %v, results %+v", sizes, results)
		}
	})
	t.Run("ordered streams stay sequential inside batches", func(t *testing.T) {
		var a []string
		r, err := consumer.NewBatch(func(_ context.Context, batch []consumer.Message) []consumer.Result {
			seen := map[string]bool{}
			results := make([]consumer.Result, len(batch))
			for i, message := range batch {
				if seen[message.Stream] {
					t.Error("same stream entered one concurrent batch twice")
				}
				seen[message.Stream] = true
				if message.Stream == "a" {
					a = append(a, message.ID)
				}
				results[i].Outcome = consumer.Success
			}
			return results
		}, consumer.WithBatch(3, time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close(ctx)
		results := r.Process(ctx, []consumer.Message{{ID: "a0", Stream: "a"}, {ID: "a1", Stream: "a"}, {ID: "b0", Stream: "b"}})
		for _, result := range results {
			if result.Outcome != consumer.Success {
				t.Fatalf("result %+v", result)
			}
		}
		if !reflect.DeepEqual(a, []string{"a0", "a1"}) {
			t.Fatalf("stream a order: %v", a)
		}
	})
}

func TestBatchContainsHandlerFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler consumer.BatchHandler
		want    error
	}{
		{"panic", func(context.Context, []consumer.Message) []consumer.Result { panic("bug") }, consumer.ErrHandlerPanic},
		{"missing result", func(context.Context, []consumer.Message) []consumer.Result { return nil }, consumer.ErrBatchResultCount},
		{"invalid outcome", func(context.Context, []consumer.Message) []consumer.Result { return []consumer.Result{{}} }, consumer.ErrInvalidOutcome},
		{"success with error", func(context.Context, []consumer.Message) []consumer.Result {
			return []consumer.Result{{Outcome: consumer.Success, Err: consumer.ErrInvalidOutcome}}
		}, consumer.ErrInvalidOutcome},
		{"retry without cause", func(context.Context, []consumer.Message) []consumer.Result {
			return []consumer.Result{{Outcome: consumer.Retry}}
		}, consumer.ErrRetryRequested},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, err := consumer.NewBatch(test.handler, consumer.WithBatch(1, 0))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(context.Background())
			results := r.Process(context.Background(), []consumer.Message{{ID: "message"}})
			if results[0].Outcome != consumer.Retry || !errors.Is(results[0].Err, test.want) {
				t.Fatalf("unexpected result %+v", results[0])
			}
		})
	}
}

func TestProcessEmptyWindowDoesNotInvokeHandler(t *testing.T) {
	r, err := consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) {
		t.Fatal("handler ran for an empty source window")
		return consumer.Retry, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	if results := r.Process(context.Background(), nil); len(results) != 0 {
		t.Fatalf("empty window returned results: %+v", results)
	}
}

func TestCloseCancelsQueuedWorkWithoutRunningIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, activeCanceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseWork := func() { releaseOnce.Do(func() { close(release) }) }
	r, err := consumer.New(func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
		if message.ID == "active" {
			close(started)
			<-ctx.Done()
			close(activeCanceled)
			<-release
			return consumer.Success, nil
		}
		t.Errorf("queued handler ran after runtime cancellation: %s", message.ID)
		return consumer.Success, nil
	}, consumer.WithQueueCapacity(1), consumer.WithUnordered())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		releaseWork()
		_ = r.Close(context.Background())
	}()
	active := make(chan []consumer.Result, 1)
	go func() { active <- r.Process(ctx, []consumer.Message{{ID: "active"}}) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("active handler did not start")
	}
	queued := make(chan []consumer.Result, 1)
	go func() { queued <- r.Process(ctx, []consumer.Message{{ID: "queued"}}) }()
	waitQueued(t, ctx, r, 1)
	closeCtx, stop := context.WithCancel(ctx)
	stop()
	if err := r.Close(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close() = %v", err)
	}
	select {
	case <-activeCanceled:
	case <-ctx.Done():
		t.Fatal("active handler did not observe runtime cancellation")
	}
	releaseWork()
	if result := <-active; result[0].Outcome != consumer.Retry || !errors.Is(result[0].Err, context.Canceled) {
		t.Fatalf("active result = %+v; want retry after forced cancellation", result)
	}
	if result := <-queued; result[0].Outcome != consumer.Retry || !errors.Is(result[0].Err, context.Canceled) {
		t.Fatalf("queued result = %+v", result)
	}
}

func TestCloseDrainsAndCancelsOnDeadline(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(map[bool]string{false: "drain", true: "deadline"}[abort], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			r, err := consumer.New(func(ctx context.Context, _ consumer.Message) (consumer.Outcome, error) {
				close(started)
				select {
				case <-release:
					return consumer.Success, nil
				case <-ctx.Done():
					return consumer.Retry, ctx.Err()
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan []consumer.Result, 1)
			go func() { done <- r.Process(ctx, []consumer.Message{{ID: "one"}}) }()
			<-started
			if abort {
				closeCtx, closeCancel := context.WithCancel(ctx)
				closeCancel()
				if err := r.Close(closeCtx); !errors.Is(err, context.Canceled) {
					t.Fatalf("close: %v", err)
				}
			} else {
				close(release)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			results := <-done
			want := consumer.Success
			if abort {
				want = consumer.Retry
			}
			if results[0].Outcome != want {
				t.Fatalf("result %+v", results[0])
			}
			if result := r.Process(ctx, []consumer.Message{{ID: "late"}}); !errors.Is(result[0].Err, consumer.ErrClosed) {
				t.Fatalf("accepted after close: %+v", result)
			}
		})
	}
}

func waitQueued(t *testing.T, ctx context.Context, r *consumer.Runtime, want int) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for r.Stats().Queued != want {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("queue did not reach expected depth")
		}
	}
}
