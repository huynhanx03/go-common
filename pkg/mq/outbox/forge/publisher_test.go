package forge

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	forgecore "github.com/huynhanx03/go-common/pkg/mq/forge"
	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

type recordingSender struct {
	mu      sync.Mutex
	key     []byte
	payload []byte
	headers []forgecore.Header
	ack     forgecore.Acknowledgment
	err     error
}

func (sender *recordingSender) SendContext(
	_ context.Context,
	key, payload []byte,
	headers []forgecore.Header,
	ack forgecore.Acknowledgment,
) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.key = append([]byte(nil), key...)
	sender.payload = append([]byte(nil), payload...)
	sender.headers = cloneHeaders(headers)
	sender.ack = ack
	return sender.err
}

func TestPublisherPreservesBodyAndAddsBoundedInfrastructureHeaders(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	publisher, err := newPublisher(map[string]routeSender{"events": sender})
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	message := outbox.Message{
		ID:          "message-1",
		Destination: "events",
		Key:         []byte("partition-key"),
		Payload:     []byte("opaque-payload"),
		Metadata: outbox.Metadata{
			CorrelationID: "cid-1",
			Attributes: []outbox.Attribute{
				{Key: "region", Value: "west"},
				{Key: "schema", Value: "v1"},
			},
		},
	}
	ack, err := publisher.Publish(context.Background(), message)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if ack.Reference != "" {
		t.Fatalf("PublishAck.Reference = %q, want empty local reference", ack.Reference)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.ack != forgecore.AckFsync {
		t.Fatalf("ack = %v, want AckFsync", sender.ack)
	}
	if !bytes.Equal(sender.key, message.Key) || !bytes.Equal(sender.payload, message.Payload) {
		t.Fatalf("body changed: key=%q payload=%q", sender.key, sender.payload)
	}
	wantHeaders := []forgecore.Header{
		{Key: []byte(HeaderMessageID), Value: []byte("message-1")},
		{Key: []byte(HeaderCorrelationID), Value: []byte("cid-1")},
		{Key: []byte(HeaderAttributePrefix + "region"), Value: []byte("west")},
		{Key: []byte(HeaderAttributePrefix + "schema"), Value: []byte("v1")},
	}
	if !equalHeaders(sender.headers, wantHeaders) {
		t.Fatalf("headers = %+v, want %+v", sender.headers, wantHeaders)
	}
}

func TestPublisherClonesRoutesAndRejectsInvalidConstruction(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	routes := map[string]routeSender{"events": sender}
	publisher, err := newPublisher(routes)
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	delete(routes, "events")
	routes["events"] = nil
	if _, err := publisher.Publish(context.Background(), validMessage()); err != nil {
		t.Fatalf("Publish after caller map mutation: %v", err)
	}

	tests := []struct {
		name   string
		routes map[string]routeSender
	}{
		{name: "nil map", routes: nil},
		{name: "empty map", routes: map[string]routeSender{}},
		{name: "empty route", routes: map[string]routeSender{"": sender}},
		{name: "unsafe route", routes: map[string]routeSender{"events\ninjected": sender}},
		{name: "nil sender", routes: map[string]routeSender{"events": nil}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newPublisher(test.routes); !errors.Is(err, ErrInvalidRoutes) {
				t.Fatalf("newPublisher error = %v, want ErrInvalidRoutes", err)
			}
		})
	}
}

func TestPublisherRejectsMissingRouteAndUnsafeMessage(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	publisher, err := newPublisher(map[string]routeSender{"events": sender})
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	tests := []struct {
		name     string
		message  outbox.Message
		wantCode string
	}{
		{name: "missing route", message: outbox.Message{ID: "message-1", Destination: "absent"}, wantCode: CodeRouteNotConfigured},
		{name: "empty message ID", message: outbox.Message{Destination: "events"}, wantCode: CodeInvalidMessage},
		{name: "unsafe cid", message: outbox.Message{ID: "message-1", Destination: "events", Metadata: outbox.Metadata{CorrelationID: "bad cid"}}, wantCode: CodeInvalidMessage},
		{name: "unsafe attribute", message: outbox.Message{ID: "message-1", Destination: "events", Metadata: outbox.Metadata{Attributes: []outbox.Attribute{{Key: "bad key", Value: "v"}}}}, wantCode: CodeInvalidMessage},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := publisher.Publish(context.Background(), test.message)
			var failure *outbox.PublishError
			if !errors.As(err, &failure) || failure.Code != test.wantCode || failure.Retryable || failure.Ambiguous {
				t.Fatalf("Publish error = %#v, want permanent code %q", err, test.wantCode)
			}
		})
	}
}

func validMessage() outbox.Message {
	return outbox.Message{ID: "message-1", Destination: "events", Payload: []byte("payload")}
}

func cloneHeaders(headers []forgecore.Header) []forgecore.Header {
	cloned := make([]forgecore.Header, len(headers))
	for index, header := range headers {
		cloned[index] = forgecore.Header{
			Key: append([]byte(nil), header.Key...), Value: append([]byte(nil), header.Value...),
		}
	}
	return cloned
}

func equalHeaders(left, right []forgecore.Header) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !bytes.Equal(left[index].Key, right[index].Key) || !bytes.Equal(left[index].Value, right[index].Value) {
			return false
		}
	}
	return true
}
