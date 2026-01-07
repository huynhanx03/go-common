package kafka

import (
	"context"
	"fmt"

	"github.com/huynhanx03/go-common/pkg/utils"

	"github.com/IBM/sarama"
)

// asyncProducer wraps sarama.AsyncProducer
type asyncProducer struct {
	producer sarama.AsyncProducer
	errCh    chan error
}

// NewProducer creates a new AsyncProducer
func NewProducer(cfg *Config) (Producer, error) {
	config := sarama.NewConfig()
	config.ClientID = cfg.ClientID

	// Reliability: Wait for all in-sync replicas to ack.
	config.Producer.RequiredAcks = sarama.WaitForAll
	// Efficiency: Use Compression (Snappy is good balance)
	config.Producer.Compression = sarama.CompressionSnappy
	// Batching: Higher throughput
	config.Producer.Flush.Frequency = utils.ToDurationMs(cfg.ProducerInfo.FlushFrequency)
	config.Producer.Flush.Bytes = cfg.ProducerInfo.FlushBytes

	// Channels must be enabled to handle returns
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true

	producer, err := sarama.NewAsyncProducer(cfg.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka async producer: %w", err)
	}

	ap := &asyncProducer{
		producer: producer,
		errCh:    make(chan error, 100), // Buffer for errors
	}

	// Start a goroutine to read from Successes and Errors channels
	go ap.handleReturns()

	return ap, nil
}

// Publish sends a message to a specific topic
func (ap *asyncProducer) Publish(ctx context.Context, topic string, key, value []byte) {
	headers := buildHeaders(ctx)

	msg := &sarama.ProducerMessage{
		Topic:   topic,
		Key:     sarama.ByteEncoder(key),
		Value:   sarama.ByteEncoder(value),
		Headers: headers,
	}

	// Non-blocking send
	select {
	case ap.producer.Input() <- msg:
		// Enqueued successfully
	case <-ctx.Done():
		// Push context error to internal error channel if context is canceled
		select {
		case ap.errCh <- ctx.Err():
		default:
			// Drop error if channel full to avoid blocking Publish
		}
	}
}

// Errors returns the error channel for the user to listen to
func (ap *asyncProducer) Errors() <-chan error {
	return ap.errCh
}

// handleReturns handles returns from the producer
func (ap *asyncProducer) handleReturns() {
	defer close(ap.errCh)
	for {
		select {
		case <-ap.producer.Successes():
			// Success hook could be added here
		case err, ok := <-ap.producer.Errors():
			if !ok {
				return
			}
			// Forward error to user
			select {
			case ap.errCh <- err:
			default:
				// Drop error if user isn't listening fast enough
			}
		}
	}
}

// Close gracefully closes the producer
func (ap *asyncProducer) Close() error {
	return ap.producer.Close()
}
