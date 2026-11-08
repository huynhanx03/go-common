//go:build benchmark

package consumer_test

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func BenchmarkRuntime(b *testing.B) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			options := []consumer.Option{consumer.WithWorkers(4), consumer.WithUnordered()}
			var processor *consumer.Runtime
			var err error
			if batch {
				processor, err = consumer.NewBatch(func(_ context.Context, messages []consumer.Message) []consumer.Result {
					results := make([]consumer.Result, len(messages))
					for i := range results {
						results[i].Outcome = consumer.Success
					}
					return results
				}, append(options, consumer.WithBatch(64, 0))...)
			} else {
				processor, err = consumer.New(func(context.Context, consumer.Message) (consumer.Outcome, error) {
					return consumer.Success, nil
				}, options...)
			}
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = processor.Close(ctx) })
			messages := make([]consumer.Message, 64)
			for i := range messages {
				messages[i] = consumer.Message{Value: make([]byte, 256)}
			}
			b.SetBytes(int64(len(messages) * 256))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, result := range processor.Process(ctx, messages) {
					if result.Outcome != consumer.Success {
						b.Fatal(result.Err)
					}
				}
			}
		})
	}
}
