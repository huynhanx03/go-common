package forge

import (
	"bytes"
	"context"
	"errors"
	"testing"

	forgecore "github.com/huynhanx03/go-common/pkg/mq/forge"
	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

func TestNewPublisherClonesConcreteRouteMapAndPersistsMessage(t *testing.T) {
	t.Parallel()

	broker, err := forgecore.NewBroker(t.TempDir())
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Errorf("Broker.Close: %v", err)
		}
	})
	producer, err := broker.NewProducer("events")
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	routes := map[string]*forgecore.Producer{"events": producer}
	publisher, err := NewPublisher(routes)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	delete(routes, "events")
	routes["events"] = nil

	message := outbox.Message{
		ID: "message-1", Destination: "events", Key: []byte("key"), Payload: []byte("payload"),
		Metadata: outbox.Metadata{
			CorrelationID: "cid-1",
			Attributes:    []outbox.Attribute{{Key: "schema", Value: "v1"}},
		},
	}
	if _, err := publisher.Publish(context.Background(), message); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	consumer, err := broker.NewConsumer("reader", "events")
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	deliveries, err := consumer.Fetch(context.Background(), 1)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("Fetch count = %d, want 1", len(deliveries))
	}
	delivery := deliveries[0]
	if !bytes.Equal(delivery.Key, message.Key) || !bytes.Equal(delivery.Value, message.Payload) {
		t.Fatalf("persisted body = (%q, %q)", delivery.Key, delivery.Value)
	}
	wantHeaders := []forgecore.Header{
		{Key: []byte(HeaderMessageID), Value: []byte("message-1")},
		{Key: []byte(HeaderCorrelationID), Value: []byte("cid-1")},
		{Key: []byte(HeaderAttributePrefix + "schema"), Value: []byte("v1")},
	}
	if !equalHeaders(delivery.Headers, wantHeaders) {
		t.Fatalf("persisted headers = %+v, want %+v", delivery.Headers, wantHeaders)
	}
}

func TestNewPublisherRejectsInvalidConcreteRoutes(t *testing.T) {
	t.Parallel()

	for _, routes := range []map[string]*forgecore.Producer{
		nil,
		{},
		{"events": nil},
		{"events\ninjected": nil},
	} {
		if _, err := NewPublisher(routes); !errors.Is(err, ErrInvalidRoutes) {
			t.Fatalf("NewPublisher(%v) error = %v, want ErrInvalidRoutes", routes, err)
		}
	}
}

func TestNewPublisherRejectsOversizedRouteTableBeforeCopying(t *testing.T) {
	routes := make(map[string]*forgecore.Producer, maximumRoutes+1)
	for index := 0; index <= maximumRoutes; index++ {
		routes[string(rune(index+1))] = nil
	}

	allocations := testing.AllocsPerRun(100, func() {
		if _, err := NewPublisher(routes); !errors.Is(err, ErrInvalidRoutes) {
			t.Fatalf("NewPublisher() error = %v, want ErrInvalidRoutes", err)
		}
	})
	if allocations != 0 {
		t.Fatalf("NewPublisher() allocations = %v, want zero before route copy", allocations)
	}
}

func TestPublishMapsErrorsFromConcreteProducer(t *testing.T) {
	t.Parallel()

	t.Run("backpressure", func(t *testing.T) {
		broker, err := forgecore.NewBroker(t.TempDir())
		if err != nil {
			t.Fatalf("NewBroker: %v", err)
		}
		t.Cleanup(func() { _ = broker.Close() })
		producer, err := broker.NewProducer("events", forgecore.WithMaxPendingBytes(1))
		if err != nil {
			t.Fatalf("NewProducer: %v", err)
		}
		assertPublishFailure(t, producer, validMessage(), CodeBackpressure, true, false)
	})

	t.Run("message too large", func(t *testing.T) {
		broker, err := forgecore.NewBroker(t.TempDir())
		if err != nil {
			t.Fatalf("NewBroker: %v", err)
		}
		t.Cleanup(func() { _ = broker.Close() })
		producer, err := broker.NewProducer("events")
		if err != nil {
			t.Fatalf("NewProducer: %v", err)
		}
		message := validMessage()
		message.Payload = make([]byte, forgecore.DefaultMaxMessageSize+1)
		assertPublishFailure(t, producer, message, CodeMessageTooLarge, false, false)
	})

	t.Run("closed", func(t *testing.T) {
		broker, err := forgecore.NewBroker(t.TempDir())
		if err != nil {
			t.Fatalf("NewBroker: %v", err)
		}
		t.Cleanup(func() { _ = broker.Close() })
		producer, err := broker.NewProducer("events")
		if err != nil {
			t.Fatalf("NewProducer: %v", err)
		}
		if err := producer.Close(); err != nil {
			t.Fatalf("Producer.Close: %v", err)
		}
		assertPublishFailure(t, producer, validMessage(), CodeClosed, true, false)
	})
}

func assertPublishFailure(
	t *testing.T,
	producer *forgecore.Producer,
	message outbox.Message,
	wantCode string,
	wantRetryable, wantAmbiguous bool,
) {
	t.Helper()
	publisher, err := NewPublisher(map[string]*forgecore.Producer{"events": producer})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	_, err = publisher.Publish(context.Background(), message)
	var failure *outbox.PublishError
	if !errors.As(err, &failure) || failure.Code != wantCode ||
		failure.Retryable != wantRetryable || failure.Ambiguous != wantAmbiguous {
		t.Fatalf("Publish error = %#v", err)
	}
}
