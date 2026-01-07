package kafka

import (
	"context"
	"fmt"

	"github.com/IBM/sarama"

	"github.com/huynhanx03/go-common/pkg/utils"
)

// syncProducer wraps sarama.SyncProducer for reliable, blocking sends.
// Use this when you MUST know if a message was successfully persisted before proceeding.
type syncProducer struct {
	producer sarama.SyncProducer
}

// NewSyncProducer creates a new SyncProducer
func NewSyncProducer(cfg *Config) (SyncProducer, error) {
	config := sarama.NewConfig()
	config.ClientID = cfg.ClientID

	// Reliability: Wait for all in-sync replicas to ack
	config.Producer.RequiredAcks = sarama.WaitForAll

	// Retry: Retry when network fails
	config.Producer.Retry.Max = cfg.ProducerInfo.MaxRetries
	config.Producer.Retry.Backoff = utils.ToDurationMs(cfg.ProducerInfo.RetryBackoff)

	// SyncProducer specific: Return Successes channel
	config.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer(cfg.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka sync producer: %w", err)
	}

	return &syncProducer{
		producer: producer,
	}, nil
}

// Publish sends a message and waits for acknowledgement.
// Returns partition, offset, and error.
func (sp *syncProducer) Publish(ctx context.Context, topic string, key, value []byte) (int32, int64, error) {
	headers := buildHeaders(ctx)

	msg := &sarama.ProducerMessage{
		Topic:   topic,
		Key:     sarama.ByteEncoder(key),
		Value:   sarama.ByteEncoder(value),
		Headers: headers,
	}

	// SyncProducer.SendMessage is blocking
	partition, offset, err := sp.producer.SendMessage(msg)
	if err != nil {
		return 0, 0, err
	}

	return partition, offset, nil
}

// Close gracefully closes the producer
func (sp *syncProducer) Close() error {
	return sp.producer.Close()
}
