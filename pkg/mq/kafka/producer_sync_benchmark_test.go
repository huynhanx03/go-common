package kafka

import (
	"context"
	"testing"
)

// BenchmarkSyncProducer_Publish measures the latency of synchronous publishing.
func BenchmarkSyncProducer_Publish(b *testing.B) {
	ctx := context.Background()
	if len(integrationBrokers) == 0 {
		b.Skip("Docker is not running or setup failed")
	}

	cfg := &Config{
		Brokers:  integrationBrokers,
		ClientID: "bench-sync",
		ProducerInfo: ProducerConfig{
			MaxRetries:      3,
			RetryBackoff:    10,
			ReturnSuccesses: true,
		},
	}
	producer, err := NewSyncProducer(cfg)
	if err != nil {
		b.Fatalf("failed to create producer: %v", err)
	}
	defer producer.Close()

	key := []byte("bench-key")
	value := []byte("bench-value")
	topic := "bench-topic-sync"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := producer.Publish(ctx, topic, key, value)
		if err != nil {
			b.Errorf("Publish failed: %v", err)
		}
	}
}
