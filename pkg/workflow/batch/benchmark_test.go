//go:build benchmark

package batch

import (
	"context"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap"
)

func BenchmarkBatchRun(b *testing.B) {
	store := newMemoryStore()
	runner, err := New(store, logger.Wrap(zap.NewNop()), Config{
		Lease: settings.Coordination{
			TTLMilliseconds:              120,
			RenewIntervalMilliseconds:    30,
			OperationTimeoutMilliseconds: 10,
			CleanupTimeoutMilliseconds:   1000,
		},
		MaxPages:    1,
		PageTimeout: 5 * time.Millisecond,
	})
	if err != nil {
		b.Fatal(err)
	}
	definition := Definition{Key: "benchmark/job", InputFingerprint: "input-v1", CursorSchema: "cursor-v1"}
	step := func(_ context.Context, checkpoint Checkpoint) (Progress, error) {
		return Progress{Cursor: []byte{byte(checkpoint.Version + 1)}, Processed: 1}, nil
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := runner.Run(ctx, definition, step); err != nil {
			b.Fatal(err)
		}
	}
}
