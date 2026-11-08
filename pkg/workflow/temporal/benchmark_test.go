//go:build benchmark

package temporal

import (
	"context"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/workflow/batch"
)

func BenchmarkCorrelationPropagation(b *testing.B) {
	propagator := &contextPropagator{}
	for _, test := range []struct {
		name          string
		requestID     string
		correlationID string
	}{
		{name: "valid_metadata", requestID: "request-42", correlationID: "operation-9"},
		{name: "absent_metadata"},
	} {
		b.Run(test.name, func(b *testing.B) {
			ctx := context.Background()
			if test.requestID != "" {
				ctx = logger.WithRequestID(ctx, test.requestID)
			}
			if test.correlationID != "" {
				ctx = logger.WithCorrelationID(ctx, test.correlationID)
			}
			header := &commonpb.Header{Fields: make(map[string]*commonpb.Payload, 2)}
			writer := identityHeaderWriter{header: header.Fields}
			reader := identityHeaderReader{header: header.Fields}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := propagator.Inject(ctx, writer); err != nil {
					b.Fatal(err)
				}
				if _, err := propagator.Extract(context.Background(), reader); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBatchWorkflow(b *testing.B) {
	input := BatchWorkflowInput{
		Definition:            batch.Definition{Key: "benchmark/job", InputFingerprint: "input-v1", CursorSchema: "cursor-v1"},
		ActivityName:          "runBenchmarkBatchSegment",
		ActivityOptions:       workflow.ActivityOptions{StartToCloseTimeout: time.Minute, HeartbeatTimeout: time.Second},
		ContinueAfterSegments: 1,
	}
	commonLogger := logger.Wrap(zap.NewNop())
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(newSDKLogger(commonLogger))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		environment := suite.NewTestWorkflowEnvironment()
		environment.RegisterWorkflowWithOptions(BatchWorkflow, workflow.RegisterOptions{Name: "BatchWorkflow"})
		environment.RegisterActivityWithOptions(func(context.Context, batch.Definition) (batch.Result, error) {
			return batch.Result{Key: input.Definition.Key, Version: 1, ProcessedCount: 1, Done: true}, nil
		}, activity.RegisterOptions{Name: input.ActivityName})
		environment.ExecuteWorkflow(BatchWorkflow, input)
		if err := environment.GetWorkflowError(); err != nil {
			b.Fatal(err)
		}
	}
}
