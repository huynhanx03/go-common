//go:build integration

package temporal

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
)

func TestTemporalIntegration(t *testing.T) {
	server := containers.StartTemporal(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	base, observed := observer.New(zap.DebugLevel)
	commonLogger := logger.Wrap(zap.New(base))
	config := settings.Temporal{Address: server.Address(), Namespace: "default", DialTimeoutSeconds: 30}
	clientA, err := Dial(ctx, commonLogger, config)
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	createSchedules(t, ctx, clientA.SDK())
	createDelayedWorkflow(t, ctx, clientA.SDK())

	var activityRuns atomic.Int32
	workerA := newIntegrationWorker(t, clientA, commonLogger, &activityRuns)
	workerAContext, stopWorkerA := context.WithCancel(context.Background())
	workerARun := runIntegrationWorker(workerAContext, workerA)

	workflowID := "restartable-timer-" + fmt.Sprint(time.Now().UnixNano())
	startContext := logger.WithRequestID(ctx, "integration-request-42")
	startContext = logger.WithCorrelationID(startContext, "outbox-job-9")
	workflowRun, err := clientA.SDK().ExecuteWorkflow(startContext, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: integrationTaskQueue,
	}, integrationTimerWorkflow, 8*time.Second)
	if err != nil {
		t.Fatalf("start durable timer workflow: %v", err)
	}
	waitForWorkflowTimer(t, ctx, clientA.SDK(), workflowID)
	if err := workerA.Shutdown(ctx); err != nil {
		t.Fatalf("stop first worker: %v", err)
	}
	stopWorkerA()
	if err := receiveWorkerResult(t, workerARun); err != nil {
		t.Fatalf("first worker Run() = %v", err)
	}
	clientA.Close()

	if err := server.Restart(ctx); err != nil {
		t.Fatalf("restart Temporal server: %v", err)
	}
	config.Address = server.Address()
	clientB, err := Dial(ctx, commonLogger, config)
	if err != nil {
		t.Fatalf("redial after server restart: %v", err)
	}
	defer clientB.Close()
	assertSchedulesPersisted(t, ctx, clientB.SDK())
	if _, err := clientB.SDK().WorkflowService().DescribeWorkflowExecution(ctx, &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: "default",
		Execution: &commonpb.WorkflowExecution{WorkflowId: workflowID},
	}); err != nil {
		t.Fatalf("timer workflow was not retained by server restart: %v", err)
	}
	if _, err := clientB.SDK().WorkflowService().DescribeWorkflowExecution(ctx, &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: "default",
		Execution: &commonpb.WorkflowExecution{WorkflowId: integrationDelayedWorkflowID},
	}); err != nil {
		t.Fatalf("delayed workflow was not retained by server restart: %v", err)
	}

	workerB := newIntegrationWorker(t, clientB, commonLogger, &activityRuns)
	workerBContext, stopWorkerB := context.WithCancel(context.Background())
	workerBRun := runIntegrationWorker(workerBContext, workerB)
	var result string
	if err := clientB.SDK().GetWorkflow(ctx, workflowID, workflowRun.GetRunID()).Get(ctx, &result); err != nil {
		t.Fatalf("wait for timer workflow after failover: %v", err)
	}
	if result != "completed" || activityRuns.Load() != 1 {
		t.Fatalf("workflow result/activity executions = %q/%d; want completed/1", result, activityRuns.Load())
	}
	if err := workerB.Shutdown(ctx); err != nil {
		t.Fatalf("stop second worker: %v", err)
	}
	stopWorkerB()
	if err := receiveWorkerResult(t, workerBRun); err != nil {
		t.Fatalf("second worker Run() = %v", err)
	}
	if err := runIntegrationWorkflowReplay(commonLogger, observed, clientB.SDK(), workflowID, workflowRun.GetRunID()); err != nil {
		t.Fatalf("replay workflow history: %v", err)
	}
}

const (
	integrationTaskQueue         = "temporal-integration-tasks"
	integrationDelayedWorkflowID = "temporal-integration-delayed"
	integrationScheduleQueue     = "temporal-schedule-integration"
)

func createSchedules(t *testing.T, ctx context.Context, sdk client.Client) {
	t.Helper()
	schedules := sdk.ScheduleClient()
	daily, err := schedules.Create(ctx, client.ScheduleOptions{
		ID: "daily-schedule",
		Spec: client.ScheduleSpec{
			CronExpressions: []string{"0 2 * * *"},
			TimeZoneName:    "UTC",
		},
		Action:        &client.ScheduleWorkflowAction{Workflow: "scheduledMarkerWorkflow", TaskQueue: integrationScheduleQueue},
		Overlap:       enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE,
		CatchupWindow: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create daily schedule: %v", err)
	}
	interval, err := schedules.Create(ctx, client.ScheduleOptions{
		ID:     "interval-schedule",
		Spec:   client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: 24 * time.Hour}}},
		Action: &client.ScheduleWorkflowAction{Workflow: "scheduledMarkerWorkflow", TaskQueue: integrationScheduleQueue},
	})
	if err != nil {
		t.Fatalf("create interval schedule: %v", err)
	}
	intervalDescription, err := interval.Describe(ctx)
	if err != nil || intervalDescription.Schedule.Spec == nil || len(intervalDescription.Schedule.Spec.Intervals) == 0 {
		t.Fatalf("interval schedule description = %#v, error = %v", intervalDescription, err)
	}

	description, err := daily.Describe(ctx)
	if err != nil {
		t.Fatalf("describe daily schedule: %v", err)
	}
	if description.Schedule.Policy.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE || description.Schedule.Policy.CatchupWindow != 5*time.Minute {
		t.Fatalf("schedule policy = %#v; want explicit overlap and catch-up settings", description.Schedule.Policy)
	}
	if err := daily.Pause(ctx, client.SchedulePauseOptions{Note: "integration pause"}); err != nil {
		t.Fatalf("pause schedule: %v", err)
	}
	description, err = daily.Describe(ctx)
	if err != nil || !description.Schedule.State.Paused {
		t.Fatalf("paused schedule = %#v, error = %v", description.Schedule.State, err)
	}
	if err := daily.Unpause(ctx, client.ScheduleUnpauseOptions{Note: "integration resume"}); err != nil {
		t.Fatalf("unpause schedule: %v", err)
	}
	if err := daily.Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		updated := input.Description.Schedule
		updated.Spec = &client.ScheduleSpec{CronExpressions: []string{"0 3 * * *"}, TimeZoneName: "UTC"}
		return &client.ScheduleUpdate{Schedule: &updated}, nil
	}}); err != nil {
		t.Fatalf("update schedule: %v", err)
	}
	if err := daily.Trigger(ctx, client.ScheduleTriggerOptions{}); err != nil {
		t.Fatalf("trigger schedule: %v", err)
	}
}

func createDelayedWorkflow(t *testing.T, ctx context.Context, sdk client.Client) {
	t.Helper()
	_, err := sdk.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:         integrationDelayedWorkflowID,
		TaskQueue:  integrationScheduleQueue,
		StartDelay: 10 * time.Minute,
	}, "scheduledMarkerWorkflow")
	if err != nil {
		t.Fatalf("start delayed workflow: %v", err)
	}
}

func assertSchedulesPersisted(t *testing.T, ctx context.Context, sdk client.Client) {
	t.Helper()
	description, err := sdk.ScheduleClient().GetHandle(ctx, "daily-schedule").Describe(ctx)
	if err != nil {
		t.Fatalf("describe persisted daily schedule: %v", err)
	}
	if description.Schedule.Spec == nil || description.Schedule.Policy == nil || description.Schedule.Policy.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE {
		t.Fatalf("persisted schedule is incomplete: %#v", description.Schedule)
	}
	if description.Info.NumActions == 0 {
		t.Fatalf("manual schedule trigger was not recorded: %#v", description.Info)
	}
}

func newIntegrationWorker(t *testing.T, temporalClient *Client, commonLogger *logger.Logger, activityRuns *atomic.Int32) *Worker {
	t.Helper()
	instance, err := NewWorker(temporalClient, settings.TemporalWorker{
		Name:      "integration-worker",
		TaskQueue: integrationTaskQueue,
	}, worker.Options{})
	if err != nil {
		t.Fatalf("NewWorker() = %v", err)
	}
	instance.SDK().RegisterWorkflowWithOptions(integrationTimerWorkflow, workflow.RegisterOptions{Name: "integrationTimerWorkflow"})
	instance.SDK().RegisterActivityWithOptions(func(ctx context.Context) error {
		activityRuns.Add(1)
		commonLogger.Info(ctx, "integration activity completed")
		return nil
	}, activity.RegisterOptions{Name: "integrationTimerActivity"})
	return instance
}

func integrationTimerWorkflow(ctx workflow.Context, delay time.Duration) (string, error) {
	workflow.GetLogger(ctx).Info("integration workflow started")
	if err := workflow.SetQueryHandler(ctx, "timer-started", func() (bool, error) { return true, nil }); err != nil {
		return "", err
	}
	if err := workflow.Sleep(ctx, delay); err != nil {
		return "", err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	if err := workflow.ExecuteActivity(ctx, "integrationTimerActivity").Get(ctx, nil); err != nil {
		return "", err
	}
	return "completed", nil
}

func runIntegrationWorker(ctx context.Context, instance *Worker) <-chan error {
	result := make(chan error, 1)
	go func() { result <- instance.Run(ctx) }()
	return result
}

func waitForWorkflowTimer(t *testing.T, ctx context.Context, sdk client.Client, workflowID string) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		value, err := sdk.QueryWorkflow(ctx, workflowID, "", "timer-started")
		if err == nil {
			var started bool
			if err := value.Get(&started); err == nil && started {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("workflow timer did not start: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func receiveWorkerResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		return fmt.Errorf("worker Run() did not return after shutdown")
	}
}

func runIntegrationWorkflowReplay(
	commonLogger *logger.Logger,
	observed *observer.ObservedLogs,
	sdk client.Client,
	workflowID string,
	runID string,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	iterator := sdk.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	history := &historypb.History{}
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			return fmt.Errorf("read workflow history: %w", err)
		}
		history.Events = append(history.Events, event)
	}
	if len(history.Events) == 0 {
		return fmt.Errorf("workflow history is empty")
	}
	initialLogs := observed.FilterMessage("integration workflow started").Len()
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		ContextPropagators: []workflow.ContextPropagator{&contextPropagator{}},
		Interceptors:       []interceptor.WorkerInterceptor{&observabilityInterceptor{}},
	})
	if err != nil {
		return fmt.Errorf("create workflow replayer: %w", err)
	}
	replayer.RegisterWorkflowWithOptions(integrationTimerWorkflow, workflow.RegisterOptions{Name: "integrationTimerWorkflow"})
	if err := replayer.ReplayWorkflowHistory(newSDKLogger(commonLogger), history); err != nil {
		return err
	}
	if got := observed.FilterMessage("integration workflow started").Len(); got != initialLogs {
		return fmt.Errorf("workflow logs changed during replay: before %d, after %d", initialLogs, got)
	}
	return nil
}
