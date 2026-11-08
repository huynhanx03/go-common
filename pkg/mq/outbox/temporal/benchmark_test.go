//go:build benchmark

package temporaloutbox

import (
	"context"
	"testing"

	"go.temporal.io/sdk/client"

	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

func BenchmarkOutboxWorkflowStartPreparation(b *testing.B) {
	starter := &benchmarkWorkflowStarter{}
	publisher, err := NewPublisher(starter, map[string]Route{
		"job": {
			Workflow:  "ProcessJob",
			TaskQueue: "jobs",
			Decode: func(_ context.Context, message outbox.Message) (any, error) {
				return message.ID, nil
			},
			WorkflowID: func(message outbox.Message) (string, error) { return message.ID, nil },
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	message := outbox.Message{ID: "job-42", Destination: "job"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := publisher.Publish(context.Background(), message); err != nil {
			b.Fatal(err)
		}
	}
}

type benchmarkWorkflowStarter struct{}

func (*benchmarkWorkflowStarter) ExecuteWorkflow(context.Context, client.StartWorkflowOptions, any, ...any) (client.WorkflowRun, error) {
	return nil, nil
}

var _ WorkflowStarter = (*benchmarkWorkflowStarter)(nil)
