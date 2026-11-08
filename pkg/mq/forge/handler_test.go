package forge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func TestRunCommitsOnlyCompletedPrefixAndReplaysRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	p, err := b.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "retry", "later"} {
		if err := p.Send([]byte("key"), []byte(value), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := b.NewConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("dependency down")
	var mu sync.Mutex
	var calls []string
	r, err := consumer.New(func(_ context.Context, m consumer.Message) (consumer.Outcome, error) {
		mu.Lock()
		calls = append(calls, string(m.Value))
		mu.Unlock()
		if string(m.Value) == "retry" {
			return consumer.Retry, failure
		}
		return consumer.Success, nil
	}, consumer.WithWorkers(3))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	if err := c.Run(ctx, r); !errors.Is(err, failure) {
		t.Fatalf("run error: %v", err)
	}
	if c.CommittedOffset() != 1 || c.Offset() != 1 {
		t.Fatalf("offset committed=%d cursor=%d", c.CommittedOffset(), c.Offset())
	}
	mu.Lock()
	for _, call := range calls {
		if call == "later" {
			t.Error("processed past retry in ordered stream")
		}
	}
	mu.Unlock()
	r2, err := consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) { return consumer.Success, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close(ctx)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx, r2) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for c.CommittedOffset() != 3 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("retry did not resume")
		}
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stop: %v", err)
	}
}

func TestRunRequiresDurableDeadLetterBeforeCommit(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing sink", true: "durable sink"}[configured], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			b, err := NewBroker(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			p, err := b.NewProducer("events", WithBatchSize(1))
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Send(nil, []byte("invalid"), nil); err != nil {
				t.Fatal(err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			var c *Consumer
			if configured {
				c, _, err = b.NewDLQConsumer("workers", "events")
			} else {
				c, err = b.NewConsumer("workers", "events")
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) {
				return consumer.DeadLetter, errors.New("invalid payload")
			})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(ctx)
			if !configured {
				if err := c.Run(ctx, r); !errors.Is(err, ErrDLQNotConfigured) {
					t.Fatalf("run: %v", err)
				}
				if c.CommittedOffset() != 0 {
					t.Fatal("committed before DLQ")
				}
				return
			}
			runCtx, stop := context.WithCancel(ctx)
			defer stop()
			done := make(chan error, 1)
			go func() { done <- c.Run(runCtx, r) }()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for c.CommittedOffset() != 1 {
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("DLQ did not settle")
				}
			}
			stop()
			<-done
			dlq, err := b.NewConsumer("inspect", "events.dlq")
			if err != nil {
				t.Fatal(err)
			}
			messages, err := dlq.Fetch(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || string(messages[0].Value) != "invalid" {
				t.Fatalf("DLQ messages: %+v", messages)
			}
		})
	}
}

func TestHandleAppliesOutcomesAndReplaysUncommittedDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := NewBroker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	p, err := b.NewProducer("events", WithBatchSize(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "second"} {
		if err := p.Send([]byte("key"), []byte(value), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	c, _, err := b.NewDLQConsumer("workers", "events")
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := c.Fetch(ctx, 1)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("initial fetch = (%d deliveries, %v)", len(deliveries), err)
	}
	outOfOrder, err := c.Fetch(ctx, 1)
	if err != nil || len(outOfOrder) != 1 || outOfOrder[0].Offset != 1 {
		t.Fatalf("next fetch = (%+v, %v)", outOfOrder, err)
	}
	called := false
	if outcome, err := c.Handle(ctx, outOfOrder[0], func(context.Context, consumer.Message) (consumer.Outcome, error) {
		called = true
		return consumer.Success, nil
	}); outcome != consumer.Retry || !errors.Is(err, ErrInvalidOffsetCommit) || called {
		t.Fatalf("out-of-order Handle() = (%v, %v), called=%t", outcome, err, called)
	}

	first := func() Delivery {
		t.Helper()
		batch, err := c.Fetch(ctx, 1)
		if err != nil || len(batch) == 0 || batch[0].Offset != 0 {
			t.Fatalf("retry did not replay first delivery: (%+v, %v)", batch, err)
		}
		return batch[0]
	}
	failure := errors.New("handler failed")
	for _, test := range []struct {
		name    string
		outcome consumer.Outcome
		handler error
		wantErr error
	}{
		{name: "retry without cause", outcome: consumer.Retry, wantErr: consumer.ErrRetryRequested},
		{name: "success with error", outcome: consumer.Success, handler: failure, wantErr: failure},
		{name: "invalid outcome", outcome: consumer.Outcome(99), wantErr: consumer.ErrInvalidOutcome},
	} {
		t.Run(test.name, func(t *testing.T) {
			if outcome, err := c.Handle(ctx, first(), func(context.Context, consumer.Message) (consumer.Outcome, error) {
				return test.outcome, test.handler
			}); outcome != consumer.Retry || !errors.Is(err, test.wantErr) {
				t.Fatalf("Handle() = (%v, %v), want retry with %v", outcome, err, test.wantErr)
			}
			if c.CommittedOffset() != 0 {
				t.Fatalf("retry advanced committed offset to %d", c.CommittedOffset())
			}
		})
	}

	if outcome, err := c.Handle(ctx, first(), func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.DeadLetter, failure
	}); outcome != consumer.DeadLetter || err != nil {
		t.Fatalf("durable dead-letter Handle() = (%v, %v)", outcome, err)
	}
	if c.CommittedOffset() != 1 {
		t.Fatalf("dead-letter committed offset = %d, want 1", c.CommittedOffset())
	}
	second, err := c.Fetch(ctx, 1)
	if err != nil || len(second) != 1 || string(second[0].Value) != "second" {
		t.Fatalf("next delivery after dead-letter = (%+v, %v)", second, err)
	}
	if outcome, err := c.Handle(ctx, second[0], func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.Success, nil
	}); outcome != consumer.Success || err != nil || c.CommittedOffset() != 2 {
		t.Fatalf("successful Handle() = (%v, %v), committed=%d", outcome, err, c.CommittedOffset())
	}
	dlq, err := b.NewConsumer("inspect", "events.dlq")
	if err != nil {
		t.Fatal(err)
	}
	quarantined, err := dlq.Fetch(ctx, 1)
	if err != nil || len(quarantined) != 1 || string(quarantined[0].Value) != "first" {
		t.Fatalf("durable dead-letter record = (%+v, %v)", quarantined, err)
	}
}
