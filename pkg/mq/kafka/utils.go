package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/IBM/sarama"
)

// buildContext creates a context from the message headers
func buildContext(headers []*sarama.RecordHeader) context.Context {
	ctx := context.Background()

	for _, h := range headers {
		key := string(h.Key)
		val := string(h.Value)

		switch key {
		case HeaderRequestID:
			ctx = context.WithValue(ctx, ContextKeyRequestID, val)
		case HeaderTraceID:
			ctx = context.WithValue(ctx, ContextKeyTraceID, val)
		}
	}

	return ctx
}

// buildHeaders extracts values from context and creates Kafka headers
func buildHeaders(ctx context.Context) []sarama.RecordHeader {
	var headers []sarama.RecordHeader

	// 1. Get RequestID
	if val, ok := ctx.Value(ContextKeyRequestID).(string); ok && val != "" {
		headers = append(headers, sarama.RecordHeader{
			Key:   []byte(HeaderRequestID),
			Value: []byte(val),
		})
	}

	// 2. Get TraceID
	if val, ok := ctx.Value(ContextKeyTraceID).(string); ok && val != "" {
		headers = append(headers, sarama.RecordHeader{
			Key:   []byte(HeaderTraceID),
			Value: []byte(val),
		})
	}

	return headers
}

// PublishJSON serializes data to JSON and publishes it to the specified topic.
// It uses a key extracted from the keyFunc or empty if nil.
func PublishJSON[T any](ctx context.Context, producer Producer, topic string, keyFunc func(T) string, data T) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	var key []byte
	if keyFunc != nil {
		key = []byte(keyFunc(data))
	}

	producer.Publish(ctx, topic, key, bytes)
	return nil
}
