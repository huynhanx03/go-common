package unleash_test

import (
	"context"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

func BenchmarkClientEvaluation(b *testing.B) {
	b.Run("boolean", func(b *testing.B) {
		client := benchmarkClient(b, toggle.Flag{Key: "plain", Enabled: true})
		ctx := context.Background()
		evaluation := toggle.EvaluationContext{UserID: "benchmark-user"}
		if _, err := client.IsEnabled(ctx, "plain", evaluation, false); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := client.IsEnabled(ctx, "plain", evaluation, false); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("targeted-rollout", func(b *testing.B) {
		client := benchmarkClient(b, toggle.Flag{Key: "targeted", Enabled: true, Rules: []toggle.Rule{{
			Rollout: 50, GroupID: "benchmark-targeted", Stickiness: "userId",
			Constraints: []toggle.Constraint{{ContextName: "region", Operator: "IN", Values: []string{"us"}}},
		}}})
		ctx := context.Background()
		evaluation := toggle.EvaluationContext{UserID: "benchmark-user", Properties: map[string]string{"region": "us"}}
		if _, err := client.IsEnabled(ctx, "targeted", evaluation, false); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := client.IsEnabled(ctx, "targeted", evaluation, false); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("variant", func(b *testing.B) {
		client := benchmarkClient(b, toggle.Flag{Key: "experiment", Enabled: true, Rules: []toggle.Rule{{
			Rollout: 100, GroupID: "benchmark-variant", Stickiness: "userId",
			Variants: []toggle.Variant{
				{Name: "control", Weight: 500, Payload: &toggle.Payload{Type: "json", Value: `{"arm":"control"}`}},
				{Name: "treatment", Weight: 500, Payload: &toggle.Payload{Type: "json", Value: `{"arm":"treatment"}`}},
			},
		}}})
		ctx := context.Background()
		evaluation := toggle.EvaluationContext{UserID: "benchmark-user"}
		if _, err := client.GetVariant(ctx, "experiment", evaluation); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := client.GetVariant(ctx, "experiment", evaluation); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchmarkClient(b *testing.B, flag toggle.Flag) toggle.Client {
	b.Helper()
	client, err := unleash.New(context.Background(), unleash.Config{
		Scope: testScope, AppName: "toggle-benchmark",
		Source: sourceFunc(func(context.Context, toggle.Scope) (toggle.Snapshot, error) {
			return toggle.Snapshot{Scope: testScope, Revision: 1, Flags: []toggle.Flag{flag}}, nil
		}),
		RefreshInterval: time.Hour, SourceTimeout: time.Second,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			b.Error(err)
		}
	})
	return client
}
