package temporal

import (
	"context"
	"os"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/common/lifecycle"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func Example_applicationSetup() {
	ctx := context.Background()
	log := logger.Wrap(zap.NewNop())
	config := settings.Temporal{
		Address:    "temporal.internal:7233",
		Namespace:  "application",
		TLSEnabled: true,
		APIKey:     os.Getenv("TEMPORAL_API_KEY"),
	}
	temporalClient, err := Dial(ctx, log, config)
	if err != nil {
		return
	}

	processor, err := NewWorker(temporalClient, settings.TemporalWorker{
		Name:      "daily-processor",
		TaskQueue: "daily-operations",
	}, worker.Options{})
	if err != nil {
		temporalClient.Close()
		return
	}
	processor.SDK().RegisterWorkflowWithOptions(dailyWorkflow, workflow.RegisterOptions{Name: "dailyWorkflow"})
	processor.SDK().RegisterActivityWithOptions(dailyActivity, activity.RegisterOptions{Name: "dailyActivity"})

	componentRunner, err := lifecycle.New(log, lifecycle.Options{})
	if err != nil {
		processor.Shutdown(ctx)
		temporalClient.Close()
		return
	}
	if err := componentRunner.Add(processor, lifecycle.ComponentOptions{Required: true}); err != nil {
		processor.Shutdown(ctx)
		temporalClient.Close()
		return
	}

	sdk := temporalClient.SDK()
	_, err = sdk.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: "daily-maintenance",
		Spec: client.ScheduleSpec{
			CronExpressions: []string{"0 2 * * *"},
			TimeZoneName:    "Asia/Ho_Chi_Minh",
		},
		Action:        &client.ScheduleWorkflowAction{Workflow: "dailyWorkflow", TaskQueue: "daily-operations"},
		Overlap:       enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		CatchupWindow: 15 * time.Minute,
	})
	if err != nil {
		processor.Shutdown(ctx)
		temporalClient.Close()
		return
	}

	_, err = sdk.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:         "delayed-maintenance-2026-11-07",
		TaskQueue:  "daily-operations",
		StartDelay: 30 * time.Minute,
	}, "dailyWorkflow")
	if err != nil {
		processor.Shutdown(ctx)
		temporalClient.Close()
		return
	}

	// Run blocks until ctx is canceled and shuts the worker down within its budget.
	_ = componentRunner.Run(ctx)
	// The runner has stopped the worker before the shared client is closed.
	temporalClient.Close()
}

func dailyWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Minute})
	return workflow.ExecuteActivity(ctx, "dailyActivity").Get(ctx, nil)
}

func dailyActivity(context.Context) error { return nil }
