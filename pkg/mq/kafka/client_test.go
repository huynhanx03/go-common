package kafka

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const (
	kafkaImage     = "apache/kafka:4.1.0"
	testTopic      = "integration-test-topic"
	testAsyncTopic = "integration-async-topic"
	testGroupID    = "integration-test-group"
	mappedPort     = "29092"
	internalPort   = "9092"
	startupTimeout = 2 * time.Minute
)

func TestClient_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Used global integrationBrokers set by TestMain
	if len(integrationBrokers) == 0 {
		t.Skip("Docker is not running or integration setup failed")
	}

	ctx := context.Background()

	cfg := &Config{
		Brokers:  integrationBrokers,
		ClientID: "test-client",
		ProducerInfo: ProducerConfig{
			MaxRetries:      3,
			RetryBackoff:    100,
			ReturnSuccesses: true,
			FlushFrequency:  100,
			FlushBytes:      1024,
		},
		ConsumerInfo: ConsumerConfig{
			SessionTimeout: 10000,
			InitialOffset:  -2, // OffsetOldest
		},
	}

	t.Run("SyncProducer", func(t *testing.T) {
		testSyncProducer(t, ctx, cfg)
	})

	t.Run("AsyncProducer", func(t *testing.T) {
		testAsyncProducer(t, ctx, cfg)
	})

	t.Run("Consumer", func(t *testing.T) {
		testConsumer(t, ctx, cfg)
	})
}

func testSyncProducer(t *testing.T, ctx context.Context, cfg *Config) {
	producer, err := NewSyncProducer(cfg)
	if err != nil {
		t.Fatalf("failed to create sync producer: %v", err)
	}
	defer producer.Close()

	partition, offset, err := producer.Publish(ctx, testTopic, []byte("sync-key"), []byte("sync-value"))
	if err != nil {
		t.Errorf("Sync Publish failed: %v", err)
	}
	t.Logf("Sync Message published to partition %d, offset %d", partition, offset)
}

func testAsyncProducer(t *testing.T, ctx context.Context, cfg *Config) {
	producer, err := NewProducer(cfg)
	if err != nil {
		t.Fatalf("failed to create async producer: %v", err)
	}
	defer producer.Close()

	for i := 0; i < 5; i++ {
		key := []byte(fmt.Sprintf("async-key-%d", i))
		value := []byte(fmt.Sprintf("async-value-%d", i))
		producer.Publish(ctx, testAsyncTopic, key, value)
	}

	select {
	case err := <-producer.Errors():
		t.Errorf("Async Producer reported error: %v", err)
	case <-time.After(1 * time.Second):
		t.Log("Async messages published (no errors returned)")
	}
}

func testConsumer(t *testing.T, ctx context.Context, cfg *Config) {
	done := make(chan struct{})
	receivedCount := 0

	handler := func(ctx context.Context, key, value []byte) error {
		k := string(key)
		v := string(value)
		t.Logf("Received message: key=%s, value=%s", k, v)

		if k == "sync-key" && v == "sync-value" {
			receivedCount++
		}

		if receivedCount >= 1 {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		return nil
	}

	errHandler := func(err error) {
		t.Logf("Consumer error: %v", err)
	}

	consumer, err := NewConsumer(cfg, testGroupID)
	if err != nil {
		t.Fatalf("failed to create consumer: %v", err)
	}
	defer consumer.Close()

	consumeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	go func() {
		if err := consumer.Start(consumeCtx, []string{testTopic}, handler, errHandler); err != nil {
			t.Errorf("Consumer Start failed: %v", err)
		}
	}()

	select {
	case <-done:
		t.Log("Consumer successfully received expected message")
	case <-consumeCtx.Done():
		t.Error("Consumer timed out waiting for message")
	}
}
