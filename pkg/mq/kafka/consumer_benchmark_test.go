package kafka

import (
	"context"
	"testing"
)

// BenchmarkConsumer_Throughput measures how fast we can consume messages.
func BenchmarkConsumer_Throughput(b *testing.B) {
	ctx := context.Background()
	if len(integrationBrokers) == 0 {
		b.Skip("Docker is not running or setup failed")
	}

	cfg := &Config{
		Brokers:  integrationBrokers,
		ClientID: "bench-consumer",
		ProducerInfo: ProducerConfig{
			FlushFrequency: 100,
			FlushBytes:     1024 * 16,
		},
		ConsumerInfo: ConsumerConfig{
			SessionTimeout: 10000,
			InitialOffset:  -2, // Oldest
		},
	}

	// 1. Prepare Consumer
	consumer, err := NewConsumer(cfg, "bench-group")
	if err != nil {
		b.Fatalf("failed to create consumer: %v", err)
	}
	defer consumer.Close()

	topic := "bench-topic-consumer"

	// 2. Prepare Data
	producer, err := NewProducer(cfg)
	if err != nil {
		b.Fatalf("failed to create producer: %v", err)
	}
	defer producer.Close()

	produceCtx, cancelProduce := context.WithCancel(ctx)
	defer cancelProduce()

	go func() {
		key := []byte("key")
		val := []byte("value")
		for {
			select {
			case <-produceCtx.Done():
				return
			default:
				producer.Publish(ctx, topic, key, val)
			}
		}
	}()

	// 3. Benchmark
	b.ResetTimer()

	done := make(chan struct{})
	count := 0

	handler := func(ctx context.Context, key, value []byte) error {
		count++
		if count >= b.N {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		return nil
	}
	errHandler := func(err error) {}

	go func() {
		consumer.Start(ctx, []string{topic}, handler, errHandler)
	}()

	<-done

	b.StopTimer()
}
