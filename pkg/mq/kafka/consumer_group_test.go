package kafka

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func TestStartRunsLegacyHandlerAndMiddlewareBeforeAcknowledging(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	marked := make(chan struct{})
	var order []string
	middleware := func(next Handler) Handler {
		return func(ctx context.Context, key, value []byte) error {
			order = append(order, "middleware-before")
			err := next(ctx, key, value)
			order = append(order, "middleware-after")
			return err
		}
	}
	client := &scriptedConsumerGroup{consume: func(ctx context.Context, topics []string, handler sarama.ConsumerGroupHandler, session *claimSession) error {
		if !reflect.DeepEqual(topics, []string{"events"}) {
			t.Errorf("topics = %v", topics)
		}
		session.onMark = func() { close(marked); cancel() }
		if err := handler.Setup(session); err != nil {
			return err
		}
		if err := handler.ConsumeClaim(session, newClaim("payload")); err != nil {
			return err
		}
		if !reflect.DeepEqual(session.marked, []int64{10}) {
			t.Errorf("committed offsets = %v", session.marked)
		}
		return handler.Cleanup(session)
	}}
	group := &consumerGroup{client: client, done: make(chan struct{}), middlewares: []Middleware{middleware}}
	if err := group.Start(ctx, []string{"events"}, func(_ context.Context, key, value []byte) error {
		if string(value) != "payload" {
			t.Errorf("handler payload = %q", value)
		}
		order = append(order, "handler")
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-marked:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy handler did not acknowledge the message")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"middleware-before", "handler", "middleware-after"}) {
		t.Fatalf("handler execution order = %v", order)
	}
}

func TestStartWithResultReportsRetryWithoutAcknowledgingAndStopsOnClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failure := errors.New("dependency unavailable")
	client := &scriptedConsumerGroup{consume: func(ctx context.Context, _ []string, handler sarama.ConsumerGroupHandler, session *claimSession) error {
		if err := handler.Setup(session); err != nil {
			return err
		}
		if err := handler.ConsumeClaim(session, newClaim("retry")); err != nil {
			return err
		}
		return handler.Cleanup(session)
	}}
	group := &consumerGroup{client: client, done: make(chan struct{})}
	reported := make(chan error, 1)
	if err := group.StartWithResult(ctx, []string{"events"}, func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.Retry, failure
	}, func(err error) { reported <- err }, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, failure) || !strings.Contains(err.Error(), "message processing") {
			t.Fatalf("reported failure = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("retry outcome was not reported")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	if len(client.lastSession.marked) != 0 {
		t.Fatalf("retried messages were acknowledged at %v", client.lastSession.marked)
	}
}

func TestLegacyStartTreatsHandlerErrorAsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("legacy handler failed")
	client := &scriptedConsumerGroup{consume: func(ctx context.Context, _ []string, handler sarama.ConsumerGroupHandler, session *claimSession) error {
		if err := handler.Setup(session); err != nil {
			return err
		}
		if err := handler.ConsumeClaim(session, newClaim("payload")); err != nil {
			return err
		}
		return handler.Cleanup(session)
	}}
	group := &consumerGroup{client: client, done: make(chan struct{})}
	reported := make(chan error, 1)
	if err := group.Start(ctx, []string{"events"}, func(context.Context, []byte, []byte) error { return failure }, func(err error) { reported <- err }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, failure) {
			t.Fatalf("reported handler error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy handler error was not reported")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	if len(client.lastSession.marked) != 0 {
		t.Fatalf("failed legacy message was acknowledged: %v", client.lastSession.marked)
	}
}

func TestStartWithResultCommitsOnlyAfterItsHandlerSucceeds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	marked := make(chan struct{})
	client := &scriptedConsumerGroup{consume: func(ctx context.Context, _ []string, handler sarama.ConsumerGroupHandler, session *claimSession) error {
		session.onMark = func() { close(marked); cancel() }
		if err := handler.Setup(session); err != nil {
			return err
		}
		if err := handler.ConsumeClaim(session, newClaim("payload")); err != nil {
			return err
		}
		return handler.Cleanup(session)
	}}
	group := &consumerGroup{client: client, done: make(chan struct{})}
	var middlewareCalls int
	middleware := func(next consumer.Handler) consumer.Handler {
		return func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
			middlewareCalls++
			return next(ctx, message)
		}
	}
	if err := group.StartWithResult(ctx, []string{"events"}, func(_ context.Context, message consumer.Message) (consumer.Outcome, error) {
		if string(message.Value) != "payload" {
			t.Errorf("handler value = %q", message.Value)
		}
		return consumer.Success, nil
	}, nil, nil, middleware); err != nil {
		t.Fatal(err)
	}
	select {
	case <-marked:
	case <-time.After(5 * time.Second):
		t.Fatal("successful handler did not commit source offset")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.lastSession.marked, []int64{10}) || middlewareCalls != 1 {
		t.Fatalf("settled offsets=%v middleware calls=%d", client.lastSession.marked, middlewareCalls)
	}
}

func TestConsumerGroupReportsConsumeFailureAndStopsBackoffOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	failure := errors.New("broker connection failed")
	client := &scriptedConsumerGroup{consume: func(context.Context, []string, sarama.ConsumerGroupHandler, *claimSession) error {
		return failure
	}}
	group := &consumerGroup{client: client, done: make(chan struct{})}
	runtime, err := consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.Success, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	reported := make(chan error, 1)
	if err := group.StartWithRuntime(ctx, []string{"events"}, runtime, func(err error) {
		reported <- err
		cancel()
	}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, failure) {
			t.Fatalf("reported broker error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("broker failure was not reported")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerGroupRetriesConsumeFailureAfterBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("temporary broker failure")
	retried := make(chan struct{})
	calls := 0
	client := &scriptedConsumerGroup{consume: func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler, _ *claimSession) error {
		calls++
		if calls == 1 {
			return failure
		}
		close(retried)
		<-ctx.Done()
		return ctx.Err()
	}}
	group := &consumerGroup{client: client, done: make(chan struct{})}
	runtime, err := consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.Success, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	reported := make(chan error, 1)
	if err := group.StartWithRuntime(ctx, []string{"events"}, runtime, func(err error) {
		reported <- err
	}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, failure) {
			t.Fatalf("reported error = %v, want initial broker failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initial consume failure was not reported")
	}
	select {
	case <-retried:
	case <-time.After(5 * time.Second):
		t.Fatal("consumer group did not retry Consume after backoff")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("Consume calls = %d, want initial call and one retry", calls)
	}
}

func TestStartRejectsMissingHandlersAndClosedGroups(t *testing.T) {
	ctx := context.Background()
	group := &consumerGroup{client: &scriptedConsumerGroup{}, done: make(chan struct{})}
	if err := group.Start(ctx, []string{"events"}, nil, nil); !errors.Is(err, consumer.ErrInvalidConfig) {
		t.Fatalf("Start(nil) error = %v", err)
	}
	if err := group.StartWithResult(ctx, []string{"events"}, nil, nil, nil); !errors.Is(err, consumer.ErrInvalidConfig) {
		t.Fatalf("StartWithResult(nil) error = %v", err)
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	if err := group.Start(ctx, []string{"events"}, func(context.Context, []byte, []byte) error { return nil }, nil); !errors.Is(err, consumer.ErrClosed) {
		t.Fatalf("Start on closed group error = %v", err)
	}
}

type scriptedConsumerGroup struct {
	sarama.ConsumerGroup
	consume     func(context.Context, []string, sarama.ConsumerGroupHandler, *claimSession) error
	lastSession *claimSession
}

func (group *scriptedConsumerGroup) Consume(ctx context.Context, topics []string, handler sarama.ConsumerGroupHandler) error {
	session := &claimSession{ctx: ctx}
	group.lastSession = session
	if group.consume != nil {
		return group.consume(ctx, topics, handler, session)
	}
	return ctx.Err()
}

func (*scriptedConsumerGroup) Close() error { return nil }
