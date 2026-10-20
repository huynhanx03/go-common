package websocket_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/websocket"
)

func validEnvelope() websocket.Envelope {
	return websocket.Envelope{
		Version:          websocket.ProtocolVersion,
		Operation:        websocket.OperationEvent,
		ID:               "event-42",
		Cursor:           "cursor-42",
		Topic:            "resource:42",
		Type:             "resource.updated.v1",
		AggregateVersion: 4,
		OccurredAt:       time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		Data:             json.RawMessage(`{"state":"ready"}`),
	}
}

func TestEnvelopeStrictRoundTrip(t *testing.T) {
	t.Parallel()

	encoded, err := websocket.EncodeEnvelope(validEnvelope(), 64<<10)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	decoded, err := websocket.DecodeEnvelope(encoded, 64<<10)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if decoded.Version != websocket.ProtocolVersion ||
		decoded.Operation != websocket.OperationEvent ||
		decoded.AggregateVersion != 4 ||
		!bytes.Equal(decoded.Data, []byte(`{"state":"ready"}`)) {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestDecodeEnvelopeRejectsAmbiguousOrOversizedFrames(t *testing.T) {
	t.Parallel()

	documents := [][]byte{
		[]byte(`{"v":1,"v":1,"op":"ping","type":"protocol.ping.v1"}`),
		[]byte(`{"v":1,"op":"ping","type":"protocol.ping.v1","unknown":true}`),
		[]byte(`{"v":1,"op":"event","type":"event.v1","data":{"key":1,"key":2}}`),
		[]byte(`{"v":1,"op":"ping","type":"protocol.ping.v1"}{"v":1}`),
		{'{', '"', 'v', '"', ':', '1', ',', '"', 'o', 'p', '"', ':', '"', 0xff, '"', '}'},
	}
	for index, document := range documents {
		if _, err := websocket.DecodeEnvelope(document, 64<<10); !errors.Is(
			err,
			websocket.ErrInvalidEnvelope,
		) {
			t.Fatalf("document[%d] error = %v", index, err)
		}
	}

	oversized := []byte(`{"v":1,"op":"ping","type":"` + strings.Repeat("x", 1024) + `"}`)
	if _, err := websocket.DecodeEnvelope(oversized, 64); !errors.Is(
		err,
		websocket.ErrMessageTooLarge,
	) {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestEnvelopeValidationIsVersionedAndClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*websocket.Envelope)
		want error
	}{
		{name: "zero version", edit: func(envelope *websocket.Envelope) { envelope.Version = 0 }, want: websocket.ErrUnsupportedVersion},
		{name: "future version", edit: func(envelope *websocket.Envelope) { envelope.Version = 2 }, want: websocket.ErrUnsupportedVersion},
		{name: "unknown operation", edit: func(envelope *websocket.Envelope) { envelope.Operation = "execute" }, want: websocket.ErrUnknownOperation},
		{name: "missing type", edit: func(envelope *websocket.Envelope) { envelope.Type = "" }, want: websocket.ErrInvalidEnvelope},
		{name: "oversize ID", edit: func(envelope *websocket.Envelope) { envelope.ID = strings.Repeat("i", 129) }, want: websocket.ErrInvalidEnvelope},
		{name: "oversize cursor", edit: func(envelope *websocket.Envelope) { envelope.Cursor = strings.Repeat("c", 257) }, want: websocket.ErrInvalidEnvelope},
		{name: "oversize topic", edit: func(envelope *websocket.Envelope) { envelope.Topic = strings.Repeat("t", 257) }, want: websocket.ErrInvalidEnvelope},
		{name: "oversize type", edit: func(envelope *websocket.Envelope) { envelope.Type = strings.Repeat("t", 129) }, want: websocket.ErrInvalidEnvelope},
		{name: "control character", edit: func(envelope *websocket.Envelope) { envelope.Topic = "topic\nsecret" }, want: websocket.ErrInvalidEnvelope},
		{name: "negative aggregate version", edit: func(envelope *websocket.Envelope) { envelope.AggregateVersion = -1 }, want: websocket.ErrInvalidEnvelope},
		{name: "non UTC occurrence", edit: func(envelope *websocket.Envelope) {
			envelope.OccurredAt = time.Date(2026, 7, 18, 12, 0, 0, 0, time.FixedZone("private", 7*60*60))
		}, want: websocket.ErrInvalidEnvelope},
		{name: "invalid data", edit: func(envelope *websocket.Envelope) { envelope.Data = json.RawMessage(`{"broken":`) }, want: websocket.ErrInvalidEnvelope},
		{name: "subscribe without topic", edit: func(envelope *websocket.Envelope) {
			envelope.Operation = websocket.OperationSubscribe
			envelope.Topic = ""
		}, want: websocket.ErrInvalidEnvelope},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			envelope := validEnvelope()
			test.edit(&envelope)
			if _, err := websocket.EncodeEnvelope(envelope, 64<<10); !errors.Is(err, test.want) {
				t.Fatalf("EncodeEnvelope error = %v, want %v", err, test.want)
			}
		})
	}

	for _, document := range []string{
		`{"v":1,"op":"event","type":"event.v1","aggregate_version":0}`,
		`{"v":1,"op":"event","type":"event.v1","aggregate_version":-1}`,
	} {
		if _, err := websocket.DecodeEnvelope([]byte(document), 64<<10); !errors.Is(
			err,
			websocket.ErrInvalidEnvelope,
		) {
			t.Fatalf("aggregate document error = %v", err)
		}
	}
}

func TestMessageDeliveryClassContracts(t *testing.T) {
	t.Parallel()

	envelope := validEnvelope()
	valid := []websocket.Message{
		{Envelope: envelope, Class: websocket.DeliveryLatestState, CoalesceKey: "resource:42"},
		{Envelope: envelope, Class: websocket.DeliveryEphemeral},
		{Envelope: envelope, Class: websocket.DeliveryCritical},
	}
	for _, message := range valid {
		if _, err := websocket.EncodeMessage(message, 64<<10); err != nil {
			t.Fatalf("EncodeMessage(%+v): %v", message, err)
		}
	}

	invalid := []websocket.Message{
		{Envelope: envelope},
		{Envelope: envelope, Class: websocket.DeliveryClass(99)},
		{Envelope: envelope, Class: websocket.DeliveryLatestState},
		{Envelope: envelope, Class: websocket.DeliveryLatestState, CoalesceKey: strings.Repeat("x", 257)},
		{Envelope: envelope, Class: websocket.DeliveryEphemeral, CoalesceKey: "unexpected"},
		{Envelope: envelope, Class: websocket.DeliveryCritical, CoalesceKey: "unexpected"},
	}
	for _, message := range invalid {
		if _, err := websocket.EncodeMessage(message, 64<<10); !errors.Is(
			err,
			websocket.ErrInvalidMessage,
		) {
			t.Fatalf("EncodeMessage(%+v) error = %v", message, err)
		}
	}
}

func TestEnvelopeLimitsAndProtocolErrors(t *testing.T) {
	t.Parallel()

	envelope := validEnvelope()
	if _, err := websocket.EncodeEnvelope(envelope, 0); !errors.Is(
		err,
		websocket.ErrInvalidOptions,
	) {
		t.Fatalf("zero limit error = %v", err)
	}
	if _, err := websocket.DecodeEnvelope([]byte(`{}`), websocket.HardMaxMessageBytes+1); !errors.Is(
		err,
		websocket.ErrInvalidOptions,
	) {
		t.Fatalf("oversized configured limit error = %v", err)
	}
	if _, err := websocket.EncodeEnvelope(envelope, 32); !errors.Is(
		err,
		websocket.ErrMessageTooLarge,
	) {
		t.Fatalf("encoded size error = %v", err)
	} else {
		if !websocket.IsProtocolError(err, websocket.CodeMessageTooLarge) {
			t.Fatalf("protocol code mismatch: %v", err)
		}
		if strings.Contains(err.Error(), "resource") {
			t.Fatalf("protocol error leaked envelope data: %v", err)
		}
	}

	var nilProtocolError *websocket.ProtocolError
	if nilProtocolError.Error() == "" || nilProtocolError.Unwrap() != nil {
		t.Fatal("nil ProtocolError methods are not total")
	}
	if websocket.IsProtocolError(errors.New("plain"), websocket.CodeInternal) {
		t.Fatal("plain error classified as ProtocolError")
	}
}
