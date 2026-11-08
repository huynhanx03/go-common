package kafka

import (
	"context"
	"fmt"

	"github.com/IBM/sarama"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func buildHeadersWithID(ctx context.Context, messageID string) []sarama.RecordHeader {
	id := logger.RequestID(ctx)
	if id == "" && messageID == "" {
		return nil
	}
	headers := make([]sarama.RecordHeader, 0, 2)
	if id != "" {
		headers = append(headers, sarama.RecordHeader{
			Key:   []byte(logger.RequestIDHeader),
			Value: []byte(id),
		})
	}
	if messageID != "" {
		headers = append(headers, sarama.RecordHeader{
			Key:   []byte(consumer.MessageIDHeader),
			Value: []byte(messageID),
		})
	}
	return headers
}

// PublishJSON serializes data to JSON and publishes it to the specified topic.
// It uses a key extracted from the keyFunc or empty if nil.
func PublishJSON[T any](ctx context.Context, producer Producer, topic string, keyFunc func(T) string, data T) error {
	payload, key, err := encodeJSON(keyFunc, data)
	if err != nil {
		return err
	}
	producer.Publish(ctx, topic, key, payload)
	return nil
}

// PublishJSONWithID serializes and publishes data with a stable message ID.
func PublishJSONWithID[T any](ctx context.Context, producer ProducerWithID, topic, messageID string, keyFunc func(T) string, data T) error {
	payload, key, err := encodeJSON(keyFunc, data)
	if err != nil {
		return err
	}
	producer.PublishWithID(ctx, topic, messageID, key, payload)
	return nil
}

func encodeJSON[T any](keyFunc func(T) string, data T) ([]byte, []byte, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal data: %w", err)
	}
	var key []byte
	if keyFunc != nil {
		key = []byte(keyFunc(data))
	}
	return payload, key, nil
}
