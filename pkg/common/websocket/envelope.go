package websocket

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	commonjson "github.com/huynhanx03/go-common/pkg/encoding/json"
)

const (
	ProtocolVersion = 1

	OperationSubscribe      = "subscribe"
	OperationUnsubscribe    = "unsubscribe"
	OperationPing           = "ping"
	OperationPong           = "pong"
	OperationAck            = "ack"
	OperationEvent          = "event"
	OperationError          = "error"
	OperationSubscribed     = "subscribed"
	OperationUnsubscribed   = "unsubscribed"
	OperationResyncRequired = "resync_required"

	// OperationResync is retained as a concise compatibility alias.
	OperationResync = OperationResyncRequired

	maxIDBytes     = 128
	maxCursorBytes = 256
	maxTopicBytes  = 256
	maxTypeBytes   = 128
)

const (
	// ProtocolTypeSubscription is the reserved type for subscription changes.
	ProtocolTypeSubscription = "protocol.subscription.v1"
	// ProtocolTypePing is the reserved type for application heartbeat probes.
	ProtocolTypePing = "protocol.ping.v1"
	// ProtocolTypePong is the reserved type for application heartbeat replies.
	ProtocolTypePong = "protocol.pong.v1"
	// ProtocolTypeError is the reserved type for protocol error envelopes.
	ProtocolTypeError = "protocol.error.v1"
	// ProtocolTypeResyncRequired is the reserved type for resynchronization notices.
	ProtocolTypeResyncRequired = "protocol.resync-required.v1"
)

var allowedOperations = map[string]struct{}{
	OperationSubscribe:      {},
	OperationUnsubscribe:    {},
	OperationPing:           {},
	OperationPong:           {},
	OperationAck:            {},
	OperationEvent:          {},
	OperationError:          {},
	OperationSubscribed:     {},
	OperationUnsubscribed:   {},
	OperationResyncRequired: {},
}

type Envelope struct {
	Version          int             `json:"v"`
	Operation        string          `json:"op"`
	ID               string          `json:"id,omitempty"`
	Cursor           string          `json:"cursor,omitempty"`
	Topic            string          `json:"topic,omitempty"`
	Type             string          `json:"type"`
	AggregateVersion int64           `json:"aggregate_version,omitempty"`
	OccurredAt       time.Time       `json:"occurred_at,omitempty"`
	Data             json.RawMessage `json:"data,omitempty"`
}

type envelopeWire struct {
	Version          int             `json:"v"`
	Operation        string          `json:"op"`
	ID               string          `json:"id,omitempty"`
	Cursor           string          `json:"cursor,omitempty"`
	Topic            string          `json:"topic,omitempty"`
	Type             string          `json:"type"`
	AggregateVersion *int64          `json:"aggregate_version,omitempty"`
	OccurredAt       *time.Time      `json:"occurred_at,omitempty"`
	Data             json.RawMessage `json:"data,omitempty"`
}

func DecodeEnvelope(frame []byte, maximum int64) (Envelope, error) {
	if maximum <= 0 || maximum > HardMaxMessageBytes {
		return Envelope{}, ErrInvalidOptions
	}
	if len(frame) == 0 {
		return Envelope{}, newProtocolError(
			ErrInvalidEnvelope,
			CodeInvalidMessage,
			false,
		)
	}
	if int64(len(frame)) > maximum {
		return Envelope{}, newProtocolError(
			ErrMessageTooLarge,
			CodeMessageTooLarge,
			false,
		)
	}

	var wire envelopeWire
	if err := commonjson.UnmarshalStrict(frame, &wire); err != nil {
		return Envelope{}, newProtocolError(
			ErrInvalidEnvelope,
			CodeInvalidMessage,
			false,
		)
	}
	if wire.AggregateVersion != nil && *wire.AggregateVersion <= 0 {
		return Envelope{}, newProtocolError(
			ErrInvalidEnvelope,
			CodeInvalidMessage,
			false,
		)
	}
	envelope := Envelope{
		Version:   wire.Version,
		Operation: wire.Operation,
		ID:        wire.ID,
		Cursor:    wire.Cursor,
		Topic:     wire.Topic,
		Type:      wire.Type,
		Data:      wire.Data,
	}
	if wire.AggregateVersion != nil {
		envelope.AggregateVersion = *wire.AggregateVersion
	}
	if wire.OccurredAt != nil {
		envelope.OccurredAt = *wire.OccurredAt
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func EncodeEnvelope(envelope Envelope, maximum int64) ([]byte, error) {
	if maximum <= 0 || maximum > HardMaxMessageBytes {
		return nil, ErrInvalidOptions
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	wire := envelopeWire{
		Version:   envelope.Version,
		Operation: envelope.Operation,
		ID:        envelope.ID,
		Cursor:    envelope.Cursor,
		Topic:     envelope.Topic,
		Type:      envelope.Type,
		Data:      envelope.Data,
	}
	if envelope.AggregateVersion != 0 {
		aggregateVersion := envelope.AggregateVersion
		wire.AggregateVersion = &aggregateVersion
	}
	if !envelope.OccurredAt.IsZero() {
		occurredAt := envelope.OccurredAt.UTC()
		wire.OccurredAt = &occurredAt
	}
	encoded, err := commonjson.Marshal(wire)
	if err != nil {
		return nil, newProtocolError(
			ErrInvalidEnvelope,
			CodeInvalidMessage,
			false,
		)
	}
	if int64(len(encoded)) > maximum {
		return nil, newProtocolError(
			ErrMessageTooLarge,
			CodeMessageTooLarge,
			false,
		)
	}
	return encoded, nil
}

func (envelope Envelope) Validate() error {
	if envelope.Version != ProtocolVersion {
		return newProtocolError(
			ErrUnsupportedVersion,
			CodeUnsupportedVersion,
			false,
		)
	}
	if _, allowed := allowedOperations[envelope.Operation]; !allowed {
		return newProtocolError(
			ErrUnknownOperation,
			CodeUnknownOperation,
			false,
		)
	}
	if !validOptionalTerm(envelope.ID, maxIDBytes) ||
		!validOptionalTerm(envelope.Cursor, maxCursorBytes) ||
		!validOptionalTerm(envelope.Topic, maxTopicBytes) ||
		!validRequiredTerm(envelope.Type, maxTypeBytes) ||
		envelope.AggregateVersion < 0 {
		return newProtocolError(
			ErrInvalidEnvelope,
			CodeInvalidMessage,
			false,
		)
	}
	if !envelope.OccurredAt.IsZero() {
		_, offset := envelope.OccurredAt.Zone()
		if offset != 0 {
			return newProtocolError(
				ErrInvalidEnvelope,
				CodeInvalidMessage,
				false,
			)
		}
	}
	if len(envelope.Data) > HardMaxMessageBytes ||
		(len(envelope.Data) > 0 && !validStrictJSON(envelope.Data)) {
		return newProtocolError(
			ErrInvalidEnvelope,
			CodeInvalidMessage,
			false,
		)
	}
	if !envelope.validOperationShape() {
		return newProtocolError(
			ErrInvalidEnvelope,
			CodeInvalidMessage,
			false,
		)
	}
	return nil
}

// validOperationShape closes the v1 envelope schema per operation. Protocol
// operations use reserved types and intentionally carry no application data.
// Events require an object payload; acknowledgements may omit data for a bare
// receipt, but when present it must also be an object.
func (envelope Envelope) validOperationShape() bool {
	noEventMetadata := envelope.AggregateVersion == 0 && envelope.OccurredAt.IsZero()
	noData := len(envelope.Data) == 0

	switch envelope.Operation {
	case OperationSubscribe, OperationUnsubscribe:
		return envelope.ID != "" &&
			envelope.Topic != "" &&
			envelope.Type == ProtocolTypeSubscription &&
			envelope.Cursor == "" &&
			noEventMetadata &&
			noData
	case OperationPing:
		return envelope.ID != "" &&
			envelope.Type == ProtocolTypePing &&
			envelope.Topic == "" &&
			envelope.Cursor == "" &&
			noEventMetadata &&
			noData
	case OperationPong:
		return envelope.ID != "" &&
			envelope.Type == ProtocolTypePong &&
			envelope.Topic == "" &&
			envelope.Cursor == "" &&
			noEventMetadata &&
			noData
	case OperationEvent:
		return envelope.ID != "" &&
			validApplicationType(envelope.Type) &&
			validJSONObject(envelope.Data)
	case OperationAck:
		return envelope.ID != "" &&
			validApplicationType(envelope.Type) &&
			envelope.AggregateVersion == 0 &&
			envelope.OccurredAt.IsZero() &&
			(noData || validJSONObject(envelope.Data))
	case OperationError:
		return envelope.Type == ProtocolTypeError &&
			envelope.Topic == "" &&
			envelope.Cursor == "" &&
			noEventMetadata &&
			validProtocolErrorData(envelope.Data)
	case OperationSubscribed, OperationUnsubscribed:
		return envelope.ID != "" &&
			envelope.Topic != "" &&
			envelope.Type == ProtocolTypeSubscription &&
			envelope.Cursor == "" &&
			noEventMetadata &&
			noData
	case OperationResyncRequired:
		return envelope.Type == ProtocolTypeResyncRequired &&
			envelope.ID == "" &&
			envelope.Cursor == "" &&
			envelope.AggregateVersion == 0 &&
			envelope.OccurredAt.IsZero() &&
			noData
	default:
		return false
	}
}

func validApplicationType(value string) bool {
	return validRequiredTerm(value, maxTypeBytes) &&
		validMessageType(value) &&
		!strings.HasPrefix(value, "protocol.")
}

func validMessageType(value string) bool {
	segmentStart := true
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9':
			segmentStart = false
		case character == '_', character == '-':
			if segmentStart {
				return false
			}
		case character == '.':
			if segmentStart {
				return false
			}
			segmentStart = true
		default:
			return false
		}
	}
	return !segmentStart
}

func validJSONObject(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) >= 2 &&
		trimmed[0] == '{' &&
		trimmed[len(trimmed)-1] == '}' &&
		validStrictJSON(trimmed)
}

func validStrictJSON(value json.RawMessage) bool {
	if !utf8.Valid(value) {
		return false
	}
	var validated commonjson.RawMessage
	return commonjson.UnmarshalStrictLimit(
		value,
		&validated,
		HardMaxMessageBytes,
	) == nil
}

func validProtocolErrorData(value json.RawMessage) bool {
	var data struct {
		Code      ErrorCode `json:"code"`
		Retryable *bool     `json:"retryable"`
	}
	if !validJSONObject(value) ||
		commonjson.UnmarshalStrict(value, &data) != nil ||
		data.Retryable == nil {
		return false
	}
	switch data.Code {
	case CodeInvalidMessage,
		CodeUnsupportedVersion,
		CodeUnknownOperation,
		CodeMessageTooLarge,
		CodeUnauthorized,
		CodeForbiddenTopic,
		CodeNotFound,
		CodeOverloaded,
		CodeShuttingDown,
		CodeInternal:
		return true
	default:
		return false
	}
}

func validRequiredTerm(value string, maximum int) bool {
	return value != "" && validOptionalTerm(value, maximum)
}

func validOptionalTerm(value string, maximum int) bool {
	if value == "" {
		return true
	}
	if len(value) > maximum ||
		!utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func IsProtocolError(err error, code ErrorCode) bool {
	var protocolError *ProtocolError
	return errors.As(err, &protocolError) && protocolError.Code == code
}
