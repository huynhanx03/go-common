//go:build benchmark

package coordination

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap"
)

func BenchmarkLeaseDo(b *testing.B) {
	adapter := benchmarkFencedAdapter{}
	client, err := NewFenced(adapter, logger.Wrap(zap.NewNop()), settings.Coordination{
		TTLMilliseconds:              1000,
		RenewIntervalMilliseconds:    500,
		OperationTimeoutMilliseconds: 100,
		CleanupTimeoutMilliseconds:   100,
		Backoff:                      settings.Backoff{InitialDelayMilliseconds: 1, MaxDelayMilliseconds: 1},
	})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := client.Do(ctx, "benchmark/resource", func(context.Context, Grant) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

type benchmarkFencedAdapter struct{}

func (benchmarkFencedAdapter) TryAcquireFenced(_ context.Context, request AcquireRequest) (FencedLease, error) {
	return benchmarkFencedLease{grant: Grant{
		Resource: request.Resource,
		Owner:    request.Owner,
		Fence:    FenceToken{Authority: "benchmark", Value: 1},
	}}, nil
}

type benchmarkFencedLease struct{ grant Grant }

func (lease benchmarkFencedLease) Grant() Grant            { return lease.grant }
func (benchmarkFencedLease) Renew(context.Context) error   { return nil }
func (benchmarkFencedLease) Release(context.Context) error { return nil }
