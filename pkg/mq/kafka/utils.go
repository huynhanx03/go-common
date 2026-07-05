package kafka

import (
	"context"
	"fmt"

	"github.com/IBM/sarama"

	"github.com/huynhanx03/go-common/pkg/cid"
	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

// buildContext creates a context from the message headers, restoring the
// correlation ID stamped by the producer so consumer logs correlate with the
// request that published the message. Messages without one get a fresh cid.
func buildContext(headers []*sarama.RecordHeader) context.Context {
	ctx := context.Background()

	for _, h := range headers {
		if string(h.Key) == cid.Header {
			ctx = cid.WithContext(ctx, string(h.Value))
			break
		}
	}

	return cid.EnsureContext(ctx)
}

// buildHeaders carries the context's correlation ID into Kafka record headers.
func buildHeaders(ctx context.Context) []sarama.RecordHeader {
	id := cid.FromContext(ctx)
	if id == "" {
		return nil
	}
	return []sarama.RecordHeader{{
		Key:   []byte(cid.Header),
		Value: []byte(id),
	}}
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
