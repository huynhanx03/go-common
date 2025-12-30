package kafka

import (
	"context"
	"testing"
)

// BenchmarkAsyncProducer_Publish measures the throughput of asynchronous publishing.
func BenchmarkAsyncProducer_Publish(b *testing.B) {
	ctx := context.Background()
	if len(integrationBrokers) == 0 {
		b.Skip("Docker is not running or setup failed")
	}

	cfg := &Config{
		Brokers:  integrationBrokers,
		ClientID: "bench-async",
		ProducerInfo: ProducerConfig{
			MaxRetries:      3,
			RetryBackoff:    10,
			ReturnSuccesses: true,
			FlushFrequency:  200,
			FlushBytes:      1024 * 64,
		},
	}
	producer, err := NewProducer(cfg)
	if err != nil {
		b.Fatalf("failed to create producer: %v", err)
	}
	defer producer.Close()

	key := []byte("bench-key")
	value := []byte("bench-value")
	topic := "bench-topic-async"

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			producer.Publish(ctx, topic, key, value)
		}
	})

	go func() {
		for range producer.Errors() {
		}
	}()
}

// BenchmarkAsyncProducer_Publish_Sequential measures single-thread async throughput
func BenchmarkAsyncProducer_Publish_Sequential(b *testing.B) {
	ctx := context.Background()
	if len(integrationBrokers) == 0 {
		b.Skip("Docker is not running or setup failed")
	}

	cfg := &Config{
		Brokers:  integrationBrokers,
		ClientID: "bench-async-seq",
		ProducerInfo: ProducerConfig{
			FlushFrequency: 100,
			FlushBytes:     1024 * 16,
		},
	}
	producer, err := NewProducer(cfg)
	if err != nil {
		b.Fatalf("failed to create producer: %v", err)
	}
	defer producer.Close()

	go func() {
		for range producer.Errors() {
		}
	}()

	key := []byte("bench-key")
	value := []byte("bench-value")
	topic := "bench-topic-async-seq"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		producer.Publish(ctx, topic, key, value)
	}
}
