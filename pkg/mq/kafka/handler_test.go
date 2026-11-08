package kafka

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func TestConsumeClaimSettlesOnlySuccessfulPrefix(t *testing.T) {
	for _, test := range []struct {
		name       string
		deadLetter bool
		sinkErr    error
		want       []int64
	}{
		{name: "retry", want: []int64{10}},
		{name: "DLQ durable", deadLetter: true, want: []int64{10, 11, 12}},
		{name: "DLQ failed", deadLetter: true, sinkErr: errors.New("sink unavailable"), want: []int64{10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var seen []string
			r, err := consumer.NewBatch(func(_ context.Context, messages []consumer.Message) []consumer.Result {
				results := make([]consumer.Result, len(messages))
				for i, m := range messages {
					seen = append(seen, string(m.Value))
					results[i].Outcome = consumer.Success
					if string(m.Value) == "second" {
						results[i] = consumer.Result{Outcome: consumer.Retry, Err: errors.New("invalid")}
						if test.deadLetter {
							results[i].Outcome = consumer.DeadLetter
						}
					}
				}
				return results
			}, consumer.WithUnordered(), consumer.WithBatch(3, time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(ctx)
			claim := newClaim("first", "second", "third")
			session := &claimSession{ctx: ctx}
			var published []string
			h := consumerGroupHandler{runtime: r, retryRequested: make(chan struct{}, 1), deadLetterHandler: func(_ context.Context, m consumer.Message, _ error) error {
				published = append(published, string(m.Value))
				return test.sinkErr
			}}
			if err := h.ConsumeClaim(session, claim); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(session.marked, test.want) {
				t.Fatalf("marked %v, want %v", session.marked, test.want)
			}
			if !reflect.DeepEqual(seen, []string{"first", "second", "third"}) {
				t.Fatalf("processed %v", seen)
			}
			if test.deadLetter && !reflect.DeepEqual(published, []string{"second"}) {
				t.Fatalf("DLQ payloads %v", published)
			}
		})
	}
}

func TestConsumeClaimRestoresCorrelationAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, err := consumer.New(func(ctx context.Context, m consumer.Message) (consumer.Outcome, error) {
		if logger.RequestID(ctx) != "request-123" {
			t.Errorf("request ID %q", logger.RequestID(ctx))
		}
		if m.ID != "kafka:events:0:10" || m.Stream != "kafka:events:0" || m.Topic != "events" {
			t.Errorf("source identity = %+v", m)
		}
		cancel()
		return consumer.Success, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	claim := newClaim("one")
	session := &claimSession{ctx: ctx}
	h := consumerGroupHandler{runtime: r, retryRequested: make(chan struct{}, 1)}
	if err := h.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if len(session.marked) != 0 {
		t.Fatalf("marked canceled work %v", session.marked)
	}
}

func TestConsumeClaimRequiresDurableSinkForDeadLetterOutcome(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.DeadLetter, errors.New("invalid event")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	var reported error
	h := consumerGroupHandler{
		runtime:        r,
		retryRequested: make(chan struct{}, 1),
		errHandler:     func(err error) { reported = err },
	}
	session := &claimSession{ctx: ctx}
	if err := h.ConsumeClaim(session, newClaim("invalid")); err != nil {
		t.Fatal(err)
	}
	if len(session.marked) != 0 {
		t.Fatalf("dead-lettered source offset was acknowledged without a sink: %v", session.marked)
	}
	if !errors.Is(reported, consumer.ErrDeadLetterHandlerRequired) {
		t.Fatalf("reported error = %v", reported)
	}
	select {
	case <-h.retryRequested:
	default:
		t.Fatal("failed dead-letter settlement did not request redelivery")
	}
}

func TestToConsumerMessageUsesProducerIdentityWhenPresent(t *testing.T) {
	message := toConsumerMessage(&sarama.ConsumerMessage{
		Topic: "events", Partition: 3, Offset: 17, Key: []byte("key"), Value: []byte("body"),
		Headers: []*sarama.RecordHeader{{Key: []byte(consumer.MessageIDHeader), Value: []byte("stable-event-id")}},
	})
	if message.ID != "stable-event-id" || message.Stream != "kafka:events:3" || message.Topic != "events" {
		t.Fatalf("shared message identity = %+v", message)
	}
}

func TestConsumerGroupCloseCancelsSourceAndKeepsInjectedRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) { return consumer.Success, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	client := &waitingGroup{started: make(chan struct{})}
	group := &consumerGroup{client: client, done: make(chan struct{})}
	if err := group.StartWithRuntime(ctx, []string{"events"}, r, nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-ctx.Done():
		t.Fatal("source did not start")
	}
	if err := group.StartWithRuntime(ctx, []string{"events"}, r, nil, nil); !errors.Is(err, ErrConsumerStarted) {
		t.Fatalf("duplicate start: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- group.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("close did not cancel source")
	}
	if result := r.Process(ctx, []consumer.Message{{ID: "still caller owned"}}); result[0].Outcome != consumer.Success {
		t.Fatalf("closed injected runtime: %+v", result)
	}
	if err := group.StartWithRuntime(ctx, []string{"events"}, r, nil, nil); !errors.Is(err, consumer.ErrClosed) {
		t.Fatalf("start after close: %v", err)
	}
}

func TestConsumeClaimFlushesPartialBatchBeforeSourceCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := consumer.NewBatch(func(ctx context.Context, messages []consumer.Message) []consumer.Result {
		if len(messages) != 1 || string(messages[0].Value) != "one" {
			t.Errorf("unexpected partial batch: %+v", messages)
		}
		if id := logger.RequestID(consumer.MessageContext(ctx, messages[0])); id != "request-123" {
			t.Errorf("request ID %q", id)
		}
		return []consumer.Result{{Outcome: consumer.Success}}
	}, consumer.WithUnordered(), consumer.WithBatch(16, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	claim := &messageClaim{messages: make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{Topic: "events", Offset: 10, Value: []byte("one"), Headers: []*sarama.RecordHeader{{Key: []byte(logger.RequestIDHeader), Value: []byte("request-123")}}}
	session := &claimSession{ctx: ctx, onMark: func() { cancel() }}
	h := consumerGroupHandler{runtime: r, retryRequested: make(chan struct{}, 1)}
	if err := h.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.marked, []int64{10}) {
		t.Fatalf("partial batch never marked: %v", session.marked)
	}
}

func TestConsumeClaimDoesNotProcessCanceledGatheringWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := consumer.NewBatch(func(context.Context, []consumer.Message) []consumer.Result {
		t.Error("processed abandoned gathering window")
		return nil
	}, consumer.WithBatch(16, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	claim := &messageClaim{messages: make(chan *sarama.ConsumerMessage)}
	session := &claimSession{ctx: ctx}
	h := consumerGroupHandler{runtime: r, retryRequested: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() { done <- h.ConsumeClaim(session, claim) }()
	guard, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	select {
	case claim.messages <- &sarama.ConsumerMessage{Topic: "events", Offset: 10}:
	case <-guard.Done():
		t.Fatal("did not receive first message")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-guard.Done():
		t.Fatal("gather did not cancel")
	}
	if len(session.marked) != 0 {
		t.Fatalf("marked abandoned messages: %v", session.marked)
	}
}

type waitingGroup struct {
	sarama.ConsumerGroup
	started chan struct{}
}

func (g *waitingGroup) Consume(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
	close(g.started)
	<-ctx.Done()
	return ctx.Err()
}
func (g *waitingGroup) Close() error { return nil }

type claimSession struct {
	ctx    context.Context
	marked []int64
	onMark func()
}

func (s *claimSession) Claims() map[string][]int32               { return map[string][]int32{"events": {0}} }
func (s *claimSession) MemberID() string                         { return "member" }
func (s *claimSession) GenerationID() int32                      { return 1 }
func (s *claimSession) MarkOffset(string, int32, int64, string)  {}
func (s *claimSession) Commit()                                  {}
func (s *claimSession) ResetOffset(string, int32, int64, string) {}
func (s *claimSession) MarkMessage(m *sarama.ConsumerMessage, _ string) {
	s.marked = append(s.marked, m.Offset)
	if s.onMark != nil {
		s.onMark()
	}
}
func (s *claimSession) Context() context.Context { return s.ctx }

type messageClaim struct{ messages chan *sarama.ConsumerMessage }

func (c *messageClaim) Topic() string                            { return "events" }
func (c *messageClaim) Partition() int32                         { return 0 }
func (c *messageClaim) InitialOffset() int64                     { return 10 }
func (c *messageClaim) HighWaterMarkOffset() int64               { return 13 }
func (c *messageClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }
func newClaim(values ...string) *messageClaim {
	c := &messageClaim{messages: make(chan *sarama.ConsumerMessage, len(values))}
	for i, value := range values {
		c.messages <- &sarama.ConsumerMessage{Topic: "events", Partition: 0, Offset: int64(10 + i), Value: []byte(value), Headers: []*sarama.RecordHeader{{Key: []byte(logger.RequestIDHeader), Value: []byte("request-123")}}}
	}
	close(c.messages)
	return c
}
