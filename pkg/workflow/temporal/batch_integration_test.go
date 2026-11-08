//go:build integration

package temporal

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	temporalerror "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	enttest "github.com/huynhanx03/go-common/pkg/testutil/ent"
	fixture "github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture"
	gen "github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent"
	"github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent/item"
	"github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent/ledger"
	"github.com/huynhanx03/go-common/pkg/workflow/batch"
)

func TestTemporalBatchIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, driver := enttest.OpenPostgres(t)
	postgres := gen.NewClient(gen.Driver(driver))
	if err := fixture.Migrate(ctx, postgres); err != nil {
		t.Fatal(err)
	}
	store := fixture.NewStore(postgres, "temporal-test-authority")
	server := containers.StartTemporal(t)
	commonLogger := logger.Wrap(zap.NewNop())
	temporalConfig := settings.Temporal{Address: server.Address(), Namespace: "default", DialTimeoutSeconds: 30}
	temporalClient, err := Dial(ctx, commonLogger, temporalConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(temporalClient.Close)

	definition := batch.Definition{Key: "temporal/integration/job", InputFingerprint: "source-v1", CursorSchema: "cursor-v1", Boundary: []byte("fixed-cutoff")}
	workflowID := "batch-recovery-" + fmt.Sprint(time.Now().UnixNano())
	queue := "batch-integration-" + fmt.Sprint(time.Now().UnixNano())
	firstPageCommitted := make(chan struct{})
	var firstAttempt atomic.Bool
	activities := newTemporalIntegrationActivities(t, store, commonLogger, definition.Key)
	workerA := newBatchIntegrationWorker(t, temporalClient, queue, activities, firstPageCommitted, &firstAttempt)
	workerAContext, stopWorkerA := context.WithCancel(context.Background())
	workerARun := runIntegrationWorker(workerAContext, workerA)

	input := BatchWorkflowInput{
		Definition:            definition,
		ActivityName:          "runDurableBatchSegment",
		ActivityOptions:       workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second, HeartbeatTimeout: 5 * time.Second, RetryPolicy: &temporalerror.RetryPolicy{InitialInterval: 100 * time.Millisecond, MaximumInterval: time.Second, MaximumAttempts: 4}},
		ContinueAfterSegments: 1,
	}
	workflowRun, err := temporalClient.SDK().ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: workflowID, TaskQueue: queue}, BatchWorkflow, input)
	if err != nil {
		t.Fatalf("start durable batch workflow: %v", err)
	}
	firstRunID := workflowRun.GetRunID()
	select {
	case <-firstPageCommitted:
	case <-ctx.Done():
		t.Fatalf("first batch page was not committed before worker restart: %v", ctx.Err())
	}
	checkpoint, err := store.Get(ctx, definition.Key)
	if err != nil || checkpoint.Version != 1 || checkpoint.ProcessedCount != 1 {
		t.Fatalf("checkpoint before worker restart = %#v, %v", checkpoint, err)
	}
	shutdownContext, shutdownCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := workerA.Shutdown(shutdownContext); err != nil {
		shutdownCancel()
		t.Fatalf("stop the first worker: %v", err)
	}
	shutdownCancel()
	stopWorkerA()
	if err := receiveWorkerResult(t, workerARun); err != nil {
		t.Fatalf("first worker Run() = %v", err)
	}

	workerB := newBatchIntegrationWorker(t, temporalClient, queue, activities, nil, &firstAttempt)
	workerBContext, stopWorkerB := context.WithCancel(context.Background())
	workerBRun := runIntegrationWorker(workerBContext, workerB)
	var result batch.Result
	if err := workflowRun.Get(ctx, &result); err != nil {
		t.Fatalf("wait for the recovered workflow: %v", err)
	}
	if result.Key != definition.Key || !result.Done || result.Version != 3 || result.ProcessedCount != 3 {
		t.Fatalf("recovered workflow result = %#v; want three committed pages", result)
	}
	assertTemporalEffects(t, ctx, postgres, definition.Key, 3)
	checkpoint, err = store.Get(ctx, definition.Key)
	if err != nil || checkpoint.Status != batch.Completed || checkpoint.Version != 3 || checkpoint.ProcessedCount != 3 {
		t.Fatalf("final checkpoint = %#v, %v", checkpoint, err)
	}

	description, err := temporalClient.SDK().WorkflowService().DescribeWorkflowExecution(ctx, &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: "default",
		Execution: &commonpb.WorkflowExecution{WorkflowId: workflowID},
	})
	if err != nil {
		t.Fatalf("describe continued workflow: %v", err)
	}
	if description.WorkflowExecutionInfo.GetExecution().GetRunId() == firstRunID || description.WorkflowExecutionInfo.GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		t.Fatalf("workflow chain ended as %#v; want a new completed run after Continue-As-New", description.WorkflowExecutionInfo)
	}

	noOpID := workflowID + "-completed-key"
	noOpInput := input
	noOpInput.Definition.Key = definition.Key
	noOpRun, err := temporalClient.SDK().ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: noOpID, TaskQueue: queue}, BatchWorkflow, noOpInput)
	if err != nil {
		t.Fatalf("start no-op workflow for completed job: %v", err)
	}
	var noOpResult batch.Result
	if err := noOpRun.Get(ctx, &noOpResult); err != nil || noOpResult != result {
		t.Fatalf("completed-key workflow = %#v, %v; want persisted terminal result %#v", noOpResult, err, result)
	}
	assertTemporalEffects(t, ctx, postgres, definition.Key, 3)

	shutdownContext, shutdownCancel = context.WithTimeout(ctx, 15*time.Second)
	if err := workerB.Shutdown(shutdownContext); err != nil {
		shutdownCancel()
		t.Fatalf("stop the recovery worker: %v", err)
	}
	shutdownCancel()
	stopWorkerB()
	if err := receiveWorkerResult(t, workerBRun); err != nil {
		t.Fatalf("recovery worker Run() = %v", err)
	}
}

func newTemporalIntegrationActivities(t *testing.T, store *fixture.Store, log *logger.Logger, key string) *BatchActivities {
	t.Helper()
	runner, err := batch.New(store, log, batch.Config{
		Lease:    settings.Coordination{TTLMilliseconds: 3000, RenewIntervalMilliseconds: 1000, OperationTimeoutMilliseconds: 300, CleanupTimeoutMilliseconds: 1000},
		MaxPages: 1, PageTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewBatchActivities(runner, func(ctx context.Context, checkpoint batch.Checkpoint) (batch.Progress, error) {
		if err := writeTemporalEffects(ctx, key, checkpoint.Version); err != nil {
			return batch.Progress{}, err
		}
		return batch.Progress{Cursor: []byte{byte(checkpoint.Version + 1)}, Processed: 1, Done: checkpoint.Version == 2}, nil
	})
}

func writeTemporalEffects(ctx context.Context, key string, page int64) error {
	tx := gen.TxFromContext(ctx)
	if tx == nil {
		return errors.New("Activity page did not receive the Ent transaction")
	}
	if _, err := tx.Item.Create().SetJobKey(key).SetItemKey(fmt.Sprintf("page-%d", page)).Save(ctx); err != nil {
		return err
	}
	_, err := tx.Ledger.Create().SetJobKey(key).SetProcessed(1).Save(ctx)
	return err
}

func newBatchIntegrationWorker(t *testing.T, temporalClient *Client, taskQueue string, activities *BatchActivities, committed chan<- struct{}, firstAttempt *atomic.Bool) *Worker {
	t.Helper()
	instance, err := NewWorker(temporalClient, settings.TemporalWorker{Name: "batch-integration", TaskQueue: taskQueue, ShutdownTimeoutSeconds: 15}, worker.Options{WorkerStopTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = instance.Shutdown(shutdownContext)
	})
	instance.SDK().RegisterWorkflowWithOptions(BatchWorkflow, workflow.RegisterOptions{Name: "BatchWorkflow"})
	instance.SDK().RegisterActivityWithOptions(func(ctx context.Context, definition batch.Definition) (batch.Result, error) {
		result, err := activities.Run(ctx, definition)
		if err != nil {
			return result, err
		}
		if result.Version == 1 && committed != nil && firstAttempt.CompareAndSwap(false, true) {
			close(committed)
			<-activity.GetWorkerStopChannel(ctx)
			return batch.Result{}, errors.New("worker stopped after checkpoint commit")
		}
		return result, nil
	}, activity.RegisterOptions{Name: "runDurableBatchSegment"})
	return instance
}

func assertTemporalEffects(t *testing.T, ctx context.Context, client *gen.Client, key string, expected int) {
	t.Helper()
	items, err := client.Item.Query().Where(item.JobKeyEQ(key)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledgerEntries, err := client.Ledger.Query().Where(ledger.JobKeyEQ(key)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items != expected || ledgerEntries != expected {
		t.Fatalf("business effects = %d items, %d ledger entries; want %d each", items, ledgerEntries, expected)
	}
}
