// Package cdc contains bounded, domain-neutral helpers for decoding
// change-data-capture protocols. It validates transport envelopes while callers
// retain ownership of source schemas, storage, checkpointing, and delivery.
package cdc

import (
	"bytes"
	"errors"
	"fmt"

	commonjson "github.com/huynhanx03/go-common/pkg/encoding/json"
)

// Operation is the closed Debezium operation set.
type Operation string

const (
	OpCreate Operation = "c"
	OpUpdate Operation = "u"
	OpDelete Operation = "d"
	OpRead   Operation = "r"
)

const (
	// DefaultDebeziumMaxBytes bounds the complete Kafka value.
	DefaultDebeziumMaxBytes = 1 << 20
	// DefaultDebeziumNestedMaxBytes bounds decoded before/after values and source metadata.
	DefaultDebeziumNestedMaxBytes = 1 << 20
)

var (
	// ErrInvalidDebeziumEvent classifies malformed or semantically invalid events.
	ErrInvalidDebeziumEvent = errors.New("cdc: invalid Debezium event")
	// ErrDebeziumTooLarge classifies raw or decoded values that exceed configured limits.
	ErrDebeziumTooLarge = errors.New("cdc: Debezium value exceeds size limit")
	// ErrTombstone reports a Kafka tombstone. It is not a delete change event.
	ErrTombstone = errors.New("cdc: Debezium tombstone")
	// ErrInvalidParseLimits reports unsafe parser limit configuration.
	ErrInvalidParseLimits = errors.New("cdc: invalid parser limits")
)

// ParseError is a bounded diagnostic. Field is selected by this package and
// never contains caller input; untrusted payload bytes are never rendered.
type ParseError struct {
	Field string
	Kind  error
}

func (err *ParseError) Error() string {
	if err == nil || err.Kind == nil {
		return "cdc: parse error"
	}
	if err.Field == "" {
		return err.Kind.Error()
	}
	return fmt.Sprintf("%s: %s", err.Kind.Error(), err.Field)
}

// Unwrap supports errors.Is without exposing decoder diagnostics or payloads.
func (err *ParseError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Kind
}

// ParseLimits bounds the complete message plus decoded change/source documents.
type ParseLimits struct {
	MaxMessageBytes int
	MaxNestedBytes  int
}

// DefaultParseLimits returns safe single-message defaults.
func DefaultParseLimits() ParseLimits {
	return ParseLimits{
		MaxMessageBytes: DefaultDebeziumMaxBytes,
		MaxNestedBytes:  DefaultDebeziumNestedMaxBytes,
	}
}

func (limits ParseLimits) validate() error {
	if limits.MaxMessageBytes <= 0 ||
		limits.MaxMessageBytes > commonjson.HardStrictMaxBytes ||
		limits.MaxNestedBytes <= 0 ||
		limits.MaxNestedBytes > commonjson.HardStrictMaxBytes {
		return ErrInvalidParseLimits
	}
	return nil
}

// DebeziumPayload is a validated Debezium change event. Source is a bounded,
// strict JSON object kept raw so callers choose an explicit connector-specific
// type without an unbounded map[string]any allocation.
type DebeziumPayload[T any] struct {
	Before *T                    `json:"before"`
	After  *T                    `json:"after"`
	Source commonjson.RawMessage `json:"source"`
	Op     Operation             `json:"op"`
	TsMs   int64                 `json:"ts_ms"`
}

// DebeziumEnvelope represents Debezium's optional schema/payload wrapper.
type DebeziumEnvelope[T any] struct {
	Payload DebeziumPayload[T] `json:"payload"`
}

// ParseDebeziumMessage parses a bounded envelope, flat event, or stringified
// before/after value using DefaultParseLimits.
func ParseDebeziumMessage[T any](data []byte) (*DebeziumPayload[T], error) {
	return ParseDebeziumMessageWithLimits[T](data, DefaultParseLimits())
}

// ParseDebeziumMessageWithLimits parses and validates one Debezium event.
// It rejects duplicate keys at every JSON depth, excessive nesting, trailing
// values, unknown operations, and operation-specific before/after violations.
func ParseDebeziumMessageWithLimits[T any](data []byte, limits ParseLimits) (*DebeziumPayload[T], error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if len(data) > limits.MaxMessageBytes {
		return nil, &ParseError{Field: "message", Kind: ErrDebeziumTooLarge}
	}

	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, ErrTombstone
	}

	root, err := decodeObject(data, limits.MaxMessageBytes)
	if err != nil {
		return nil, invalidDebezium("message")
	}

	payloadObject := root
	if payloadRaw, wrapped := root["payload"]; wrapped {
		for _, flatField := range []string{"before", "after", "source", "op", "ts_ms"} {
			if _, mixed := root[flatField]; mixed {
				return nil, invalidDebezium("message format")
			}
		}
		if bytes.Equal(bytes.TrimSpace(payloadRaw), []byte("null")) {
			return nil, ErrTombstone
		}
		payloadObject, err = decodeObject(payloadRaw, limits.MaxMessageBytes)
		if err != nil {
			return nil, invalidDebezium("payload")
		}
	}

	beforeRaw, beforePresent := payloadObject["before"]
	afterRaw, afterPresent := payloadObject["after"]
	sourceRaw, sourcePresent := payloadObject["source"]
	opRaw, opPresent := payloadObject["op"]
	timestampRaw, timestampPresent := payloadObject["ts_ms"]
	if !beforePresent || !afterPresent || !sourcePresent || !opPresent || !timestampPresent {
		return nil, invalidDebezium("required fields")
	}
	sourceDocument := bytes.TrimSpace(sourceRaw)
	if len(sourceDocument) > limits.MaxNestedBytes {
		return nil, &ParseError{Field: "source", Kind: ErrDebeziumTooLarge}
	}
	if bytes.Equal(sourceDocument, []byte("null")) {
		return nil, invalidDebezium("source")
	}
	if _, err := decodeObject(sourceDocument, limits.MaxNestedBytes); err != nil {
		return nil, invalidDebezium("source")
	}

	var op Operation
	if err := commonjson.UnmarshalStrictLimit(opRaw, &op, limits.MaxNestedBytes); err != nil {
		return nil, invalidDebezium("operation")
	}
	switch op {
	case OpCreate, OpUpdate, OpDelete, OpRead:
	default:
		return nil, invalidDebezium("operation")
	}

	var timestamp int64
	if err := commonjson.UnmarshalStrictLimit(timestampRaw, &timestamp, limits.MaxNestedBytes); err != nil ||
		timestamp < 0 {
		return nil, invalidDebezium("timestamp")
	}

	before, err := parseRawField[T](beforeRaw, limits.MaxNestedBytes)
	if err != nil {
		return nil, err
	}
	after, err := parseRawField[T](afterRaw, limits.MaxNestedBytes)
	if err != nil {
		return nil, err
	}
	if err := validateOperationFields(op, before != nil, after != nil); err != nil {
		return nil, err
	}

	source := make(commonjson.RawMessage, len(sourceDocument))
	copy(source, sourceDocument)
	return &DebeziumPayload[T]{
		Before: before,
		After:  after,
		Source: source,
		Op:     op,
		TsMs:   timestamp,
	}, nil
}

func decodeObject(data []byte, maximum int) (map[string]commonjson.RawMessage, error) {
	var object map[string]commonjson.RawMessage
	if err := commonjson.UnmarshalStrictLimit(data, &object, maximum); err != nil || object == nil {
		return nil, ErrInvalidDebeziumEvent
	}
	return object, nil
}

func parseRawField[T any](raw commonjson.RawMessage, maximum int) (*T, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) == 0 {
		return nil, invalidDebezium("change value")
	}

	document := trimmed
	if trimmed[0] == '"' {
		var stringified string
		// The containing event has already passed strict structural validation.
		// Decode the JSON string first, then apply the nested decoded-byte cap.
		if err := commonjson.Unmarshal(trimmed, &stringified); err != nil {
			return nil, invalidDebezium("stringified change value")
		}
		if len(stringified) > maximum {
			return nil, &ParseError{Field: "nested change value", Kind: ErrDebeziumTooLarge}
		}
		document = []byte(stringified)
	}
	if len(document) > maximum {
		return nil, &ParseError{Field: "change value", Kind: ErrDebeziumTooLarge}
	}

	// Validate duplicate keys, nesting, UTF-8, and trailing values without
	// forcing callers' row structs to enumerate every source column.
	var validated commonjson.RawMessage
	if err := commonjson.UnmarshalStrictLimit(document, &validated, maximum); err != nil {
		return nil, invalidDebezium("change value")
	}
	var value T
	if err := commonjson.Unmarshal(validated, &value); err != nil {
		return nil, invalidDebezium("change value")
	}
	return &value, nil
}

func validateOperationFields(op Operation, hasBefore, hasAfter bool) error {
	valid := false
	switch op {
	case OpCreate, OpRead:
		valid = !hasBefore && hasAfter
	case OpUpdate:
		valid = hasBefore && hasAfter
	case OpDelete:
		valid = hasBefore && !hasAfter
	}
	if !valid {
		return invalidDebezium("operation fields")
	}
	return nil
}

func invalidDebezium(field string) error {
	return &ParseError{Field: field, Kind: ErrInvalidDebeziumEvent}
}
