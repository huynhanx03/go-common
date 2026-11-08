package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	"github.com/huynhanx03/go-common/pkg/logger"
)

func TestSyncProducerPublishesPayloadIdentityAndReturnsBrokerResult(t *testing.T) {
	config := sarama.NewConfig()
	producer := mocks.NewSyncProducer(t, config)
	producer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(func(message *sarama.ProducerMessage) error {
		if message.Topic != "events" {
			t.Errorf("topic = %q", message.Topic)
		}
		key, err := message.Key.Encode()
		if err != nil || string(key) != "account-7" {
			t.Errorf("key = %q, error = %v", key, err)
		}
		value, err := message.Value.Encode()
		if err != nil || string(value) != "created" {
			t.Errorf("value = %q, error = %v", value, err)
		}
		if got := messageHeader(message.Headers, logger.RequestIDHeader); got != "request-123" {
			t.Errorf("request identity = %q", got)
		}
		if got := messageHeader(message.Headers, "message-id"); got != "event-42" {
			t.Errorf("message identity = %q", got)
		}
		return nil
	})
	wrapper := &syncProducer{producer: producer}
	ctx := logger.WithRequestID(context.Background(), "request-123")
	partition, offset, err := wrapper.PublishWithID(ctx, "events", "event-42", []byte("account-7"), []byte("created"))
	if err != nil || partition < 0 || offset != 1 {
		t.Fatalf("PublishWithID() = (%d, %d, %v)", partition, offset, err)
	}
	if err := wrapper.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSyncProducerReturnsPublishFailure(t *testing.T) {
	failure := errors.New("broker rejected record")
	producer := mocks.NewSyncProducer(t, sarama.NewConfig())
	producer.ExpectSendMessageAndFail(failure)
	wrapper := &syncProducer{producer: producer}
	partition, offset, err := wrapper.Publish(context.Background(), "events", nil, []byte("body"))
	if partition != 0 || offset != 0 || !errors.Is(err, failure) {
		t.Fatalf("Publish() = (%d, %d, %v)", partition, offset, err)
	}
	if err := wrapper.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncProducerPublishesAndForwardsBrokerErrors(t *testing.T) {
	t.Run("successful publish carries request and message identities", func(t *testing.T) {
		config := sarama.NewConfig()
		config.Producer.Return.Successes = true
		config.Producer.Return.Errors = true
		producer := mocks.NewAsyncProducer(t, config)
		producer.ExpectInputWithMessageCheckerFunctionAndSucceed(func(message *sarama.ProducerMessage) error {
			if message.Topic != "events" {
				t.Errorf("topic = %q", message.Topic)
			}
			key, err := message.Key.Encode()
			if err != nil || string(key) != "account-7" {
				t.Errorf("key = %q, error = %v", key, err)
			}
			value, err := message.Value.Encode()
			if err != nil || string(value) != "created" {
				t.Errorf("value = %q, error = %v", value, err)
			}
			if got := messageHeader(message.Headers, logger.RequestIDHeader); got != "request-123" {
				t.Errorf("request identity = %q", got)
			}
			if got := messageHeader(message.Headers, "message-id"); got != "event-42" {
				t.Errorf("message identity = %q", got)
			}
			return nil
		})
		wrapper := newAsyncProducer(producer)
		wrapper.PublishWithID(logger.WithRequestID(context.Background(), "request-123"), "events", "event-42", []byte("account-7"), []byte("created"))
		if err := wrapper.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("producer failure reaches the caller error channel", func(t *testing.T) {
		failure := errors.New("broker rejected record")
		config := sarama.NewConfig()
		config.Producer.Return.Successes = true
		config.Producer.Return.Errors = true
		producer := mocks.NewAsyncProducer(t, config)
		producer.ExpectInputAndFail(failure)
		wrapper := newAsyncProducer(producer)
		wrapper.Publish(context.Background(), "events", nil, []byte("body"))
		select {
		case err := <-wrapper.Errors():
			if !errors.Is(err, failure) {
				t.Fatalf("asynchronous publish error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("asynchronous publish error was not forwarded")
		}
		if err := wrapper.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAsyncProducerReportsCanceledPublishWithoutBlocking(t *testing.T) {
	producer := &idleAsyncProducer{
		input:     make(chan *sarama.ProducerMessage),
		successes: make(chan *sarama.ProducerMessage),
		errors:    make(chan *sarama.ProducerError),
	}
	wrapper := newAsyncProducer(producer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wrapper.Publish(ctx, "events", nil, []byte("body"))
	if err := <-wrapper.Errors(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publish error = %v", err)
	}
	if err := wrapper.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncErrorDeliveryRemainsBoundedWhenCallerDoesNotDrain(t *testing.T) {
	t.Run("canceled publish does not block when its error buffer is full", func(t *testing.T) {
		producer := &idleAsyncProducer{
			input:     make(chan *sarama.ProducerMessage),
			successes: make(chan *sarama.ProducerMessage),
			errors:    make(chan *sarama.ProducerError),
		}
		wrapper := &asyncProducer{producer: producer, errCh: make(chan error, 1)}
		queued := errors.New("earlier publish failure")
		wrapper.errCh <- queued
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		wrapper.Publish(ctx, "events", nil, nil)
		if len(wrapper.Errors()) != 1 || !errors.Is(<-wrapper.Errors(), queued) {
			t.Fatal("full error buffer did not retain its queued failure")
		}
		if err := wrapper.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("broker error is dropped rather than blocking the return loop", func(t *testing.T) {
		failure, queued := errors.New("broker failure"), errors.New("earlier failure")
		returned := make(chan *sarama.ProducerError, 1)
		returned <- &sarama.ProducerError{Err: failure}
		producer := &idleAsyncProducer{
			input:     make(chan *sarama.ProducerMessage),
			successes: make(chan *sarama.ProducerMessage),
			errors:    returned,
		}
		wrapper := &asyncProducer{producer: producer, errCh: make(chan error, 1)}
		wrapper.errCh <- queued
		finished := make(chan struct{})
		go func() { wrapper.handleReturns(); close(finished) }()
		if err := wrapper.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("return loop blocked on a full caller error buffer")
		}
		if got := <-wrapper.Errors(); !errors.Is(got, queued) {
			t.Fatalf("queued error = %v", got)
		}
		if got, ok := <-wrapper.Errors(); ok {
			t.Fatalf("full-buffer broker error should have been dropped, got %v", got)
		}
	})
}

func TestJSONPublishEncodesKeysAndSkipsUnencodablePayloads(t *testing.T) {
	t.Run("JSON body and partition key are sent", func(t *testing.T) {
		producer := &captureProducer{}
		err := PublishJSON(context.Background(), producer, "events", func(event struct {
			Account string `json:"account"`
		}) string {
			return event.Account
		}, struct {
			Account string `json:"account"`
		}{Account: "account-7"})
		if err != nil {
			t.Fatal(err)
		}
		if producer.topic != "events" || string(producer.key) != "account-7" || string(producer.value) != `{"account":"account-7"}` {
			t.Fatalf("published topic=%q key=%q body=%q", producer.topic, producer.key, producer.value)
		}
	})

	t.Run("message ID is preserved by JSON publish", func(t *testing.T) {
		producer := &captureProducer{}
		err := PublishJSONWithID(context.Background(), producer, "events", "event-42", nil, map[string]string{"kind": "created"})
		if err != nil {
			t.Fatal(err)
		}
		if producer.messageID != "event-42" || producer.key != nil || producer.value == nil {
			t.Fatalf("published identity=%q key=%q body=%q", producer.messageID, producer.key, producer.value)
		}
	})

	t.Run("marshal error with ID does not publish", func(t *testing.T) {
		producer := &captureProducer{}
		if err := PublishJSONWithID(context.Background(), producer, "events", "event-42", nil, make(chan int)); err == nil {
			t.Fatal("channel payload unexpectedly encoded as JSON")
		}
		if producer.calls != 0 {
			t.Fatalf("published invalid payload %d times", producer.calls)
		}
	})

	t.Run("marshal error does not publish", func(t *testing.T) {
		producer := &captureProducer{}
		if err := PublishJSON(context.Background(), producer, "events", nil, make(chan int)); err == nil {
			t.Fatal("channel payload unexpectedly encoded as JSON")
		}
		if producer.calls != 0 {
			t.Fatalf("published invalid payload %d times", producer.calls)
		}
	})
}

func TestProducerConstructorsReturnBrokerConfigurationErrors(t *testing.T) {
	cfg := &Config{ClientID: "test-client", ConsumerInfo: ConsumerConfig{SessionTimeout: 10, MaxProcessingTime: 5}}
	if producer, err := NewProducer(cfg); err == nil || producer != nil {
		t.Fatalf("NewProducer() = (%v, %v), want broker configuration error", producer, err)
	}
	if producer, err := NewSyncProducer(cfg); err == nil || producer != nil {
		t.Fatalf("NewSyncProducer() = (%v, %v), want broker configuration error", producer, err)
	}
	if group, err := NewConsumer(cfg, "workers"); err == nil || group != nil {
		t.Fatalf("NewConsumer() = (%v, %v), want broker configuration error", group, err)
	}
}

func newAsyncProducer(producer sarama.AsyncProducer) *asyncProducer {
	wrapper := &asyncProducer{producer: producer, errCh: make(chan error, 100)}
	go wrapper.handleReturns()
	return wrapper
}

func messageHeader(headers []sarama.RecordHeader, key string) string {
	for _, header := range headers {
		if string(header.Key) == key {
			return string(header.Value)
		}
	}
	return ""
}

type captureProducer struct {
	topic     string
	messageID string
	key       []byte
	value     []byte
	calls     int
}

func (producer *captureProducer) Publish(_ context.Context, topic string, key, value []byte) {
	producer.calls++
	producer.topic, producer.key, producer.value = topic, key, value
}

func (producer *captureProducer) PublishWithID(_ context.Context, topic, messageID string, key, value []byte) {
	producer.calls++
	producer.topic, producer.messageID, producer.key, producer.value = topic, messageID, key, value
}

func (*captureProducer) Errors() <-chan error { return nil }
func (*captureProducer) Close() error         { return nil }

type idleAsyncProducer struct {
	sarama.AsyncProducer
	input     chan *sarama.ProducerMessage
	successes chan *sarama.ProducerMessage
	errors    chan *sarama.ProducerError
}

func (producer *idleAsyncProducer) Input() chan<- *sarama.ProducerMessage { return producer.input }

func (producer *idleAsyncProducer) Successes() <-chan *sarama.ProducerMessage {
	return producer.successes
}

func (producer *idleAsyncProducer) Errors() <-chan *sarama.ProducerError { return producer.errors }

func (producer *idleAsyncProducer) Close() error {
	close(producer.errors)
	return nil
}
