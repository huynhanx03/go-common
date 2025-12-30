package cdc

import (
	"encoding/json"
	"fmt"
)

// Operation represents the type of operation in a Debezium event
type Operation string

const (
	OpCreate Operation = "c"
	OpUpdate Operation = "u"
	OpDelete Operation = "d"
	OpRead   Operation = "r" // Snapshot read
)

// DebeziumPayload represents the standard Debezium change event structure
type DebeziumPayload[T any] struct {
	Before *T        `json:"before"`
	After  *T        `json:"after"`
	Source any       `json:"source"`
	Op     Operation `json:"op"`
	TsMs   int64     `json:"ts_ms"`
}

// DebeziumEnvelope represents the outer wrapper if Debezium is configured with envelopes
type DebeziumEnvelope[T any] struct {
	Payload DebeziumPayload[T] `json:"payload"`
}

// OIDWrapper is a helper to parse MongoDB ObjectId from Key
type OIDWrapper struct {
	OID string `json:"$oid"`
}

// unexported intermediary structs to keep public API clean
type rawDebeziumPayload struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
	Source any             `json:"source"`
	Op     Operation       `json:"op"`
	TsMs   int64           `json:"ts_ms"`
}

// ParseDebeziumMessage parses a raw Kafka message into a meaningful payload
func ParseDebeziumMessage[T any](data []byte) (*DebeziumPayload[T], error) {
	// Fast Path: Try Standard Envelope Format directly
	var envelope DebeziumEnvelope[T]
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Payload.Op != "" && envelope.Payload.After != nil {
		return &envelope.Payload, nil
	}

	// Slow Path: Handle Stringified JSON (common in MongoDB Source) or Flat Format
	var raw rawDebeziumPayload

	// Try Envelope first with raw payload
	var envWrapper struct {
		Payload rawDebeziumPayload `json:"payload"`
	}
	if err := json.Unmarshal(data, &envWrapper); err == nil && envWrapper.Payload.Op != "" {
		raw = envWrapper.Payload
	} else {
		// Try Flat format
		if err := json.Unmarshal(data, &raw); err != nil || raw.Op == "" {
			return nil, fmt.Errorf("unknown debezium message format")
		}
	}

	// Convert raw payload to typed payload
	result := &DebeziumPayload[T]{
		Source: raw.Source,
		Op:     raw.Op,
		TsMs:   raw.TsMs,
	}

	var err error
	if result.Before, err = parseRawField[T](raw.Before); err != nil {
		return nil, err
	}
	if result.After, err = parseRawField[T](raw.After); err != nil {
		return nil, err
	}

	return result, nil
}

// parseRawField helper to unmarshal RawMessage into *T, handling stringified JSON
func parseRawField[T any](raw json.RawMessage) (*T, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// JSON String "{\"some\": \"json\"}"
	if len(raw) > 0 && raw[0] == '"' {
		var jsonStr string
		if err := json.Unmarshal(raw, &jsonStr); err != nil {
			return nil, err
		}
		var t T
		if err := json.Unmarshal([]byte(jsonStr), &t); err != nil {
			return nil, err
		}
		return &t, nil
	}

	// Standard JSON Object {...}
	var t T
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
