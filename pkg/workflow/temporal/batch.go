package temporal

import (
	"context"
	"errors"
	"math"
	"time"

	"go.temporal.io/sdk/activity"
	temporalerror "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"github.com/huynhanx03/go-common/pkg/workflow/batch"
)

// BatchActivities runs one bounded batch segment using the durable Store state.
type BatchActivities struct {
	runner *batch.Runner
	step   batch.Step
}

// NewBatchActivities binds one batch runner and its application-owned page operation.
func NewBatchActivities(runner *batch.Runner, step batch.Step) *BatchActivities {
	return &BatchActivities{runner: runner, step: step}
}

// Run executes one batch segment and heartbeats only persisted progress.
func (a *BatchActivities) Run(ctx context.Context, definition batch.Definition) (batch.Result, error) {
	if a == nil || a.runner == nil || a.step == nil || ctx == nil || !validBatchDefinition(definition) {
		return batch.Result{Key: definition.Key}, nonRetryableBatchError(ErrInvalidBatchWorkflowInput)
	}
	info := activity.GetInfo(ctx)
	if info.HeartbeatTimeout <= 0 {
		return batch.Result{Key: definition.Key}, nonRetryableBatchError(ErrBatchHeartbeatBudget)
	}
	budget, ok := requiredBatchHeartbeatBudget(a.runner.Config())
	if !ok || info.HeartbeatTimeout <= budget {
		return batch.Result{Key: definition.Key}, nonRetryableBatchError(ErrBatchHeartbeatBudget)
	}
	activity.RecordHeartbeat(ctx, batchHeartbeat{Key: definition.Key})
	result, err := a.runner.Run(ctx, definition, a.step, batch.WithProgressObserver(func(_ context.Context, committed batch.Result) {
		activity.RecordHeartbeat(ctx, batchHeartbeat{
			Key:            committed.Key,
			Version:        committed.Version,
			ProcessedCount: committed.ProcessedCount,
		})
	}))
	if err != nil && permanentBatchError(err) {
		return result, nonRetryableBatchError(err)
	}
	return result, err
}

// BatchWorkflowInput contains project-owned Activity registration and retry policy.
type BatchWorkflowInput struct {
	Definition            batch.Definition
	ActivityName          string
	ActivityOptions       workflow.ActivityOptions
	ContinueAfterSegments int
	// LastResult carries the latest successful segment through Continue-As-New.
	// Leave it zero when starting a new workflow.
	LastResult batch.Result
}

// BatchWorkflow runs bounded Activity segments and continues as new to cap history.
func BatchWorkflow(ctx workflow.Context, input BatchWorkflowInput) (batch.Result, error) {
	if err := validateBatchWorkflowInput(input); err != nil {
		return batch.Result{Key: input.Definition.Key}, temporalerror.NewNonRetryableApplicationError(
			ErrInvalidBatchWorkflowInput.Error(), batchWorkflowInputErrorType, err,
		)
	}
	progress := input.LastResult
	if progress.Key == "" {
		progress.Key = input.Definition.Key
	}
	if err := workflow.SetQueryHandler(ctx, BatchProgressQueryName, func() (batch.Result, error) {
		return progress, nil
	}); err != nil {
		return progress, err
	}

	ctx = workflow.WithActivityOptions(ctx, input.ActivityOptions)
	segments := 0
	for {
		var result batch.Result
		if err := workflow.ExecuteActivity(ctx, input.ActivityName, input.Definition).Get(ctx, &result); err != nil {
			return progress, err
		}
		progress = result
		input.LastResult = result
		if result.Done {
			return result, nil
		}
		segments++
		if segments >= input.ContinueAfterSegments || workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			return result, workflow.NewContinueAsNewError(ctx, BatchWorkflow, input)
		}
	}
}

type batchHeartbeat struct {
	Key            string
	Version        int64
	ProcessedCount int64
}

func validateBatchWorkflowInput(input BatchWorkflowInput) error {
	options := input.ActivityOptions
	if !validBatchDefinition(input.Definition) || input.ActivityName == "" || input.ContinueAfterSegments <= 0 ||
		options.ActivityID != "" || options.StartToCloseTimeout <= 0 && options.ScheduleToCloseTimeout <= 0 ||
		options.HeartbeatTimeout <= 0 || options.ScheduleToStartTimeout < 0 || options.StartToCloseTimeout < 0 ||
		options.ScheduleToCloseTimeout < 0 || options.HeartbeatTimeout < 0 {
		return ErrInvalidBatchWorkflowInput
	}
	lastResult := input.LastResult
	if lastResult.Version < 0 || lastResult.ProcessedCount < 0 || lastResult.Done {
		return ErrInvalidBatchWorkflowInput
	}
	if lastResult.Key == "" {
		if lastResult.Version != 0 || lastResult.ProcessedCount != 0 {
			return ErrInvalidBatchWorkflowInput
		}
	} else if lastResult.Key != input.Definition.Key {
		return ErrInvalidBatchWorkflowInput
	}
	return nil
}

func validBatchDefinition(definition batch.Definition) bool {
	return definition.Key != "" && definition.InputFingerprint != "" && definition.CursorSchema != ""
}

func requiredBatchHeartbeatBudget(config batch.Config) (time.Duration, bool) {
	operation := time.Duration(config.Lease.OperationTimeoutMilliseconds) * time.Millisecond
	acquire := time.Duration(config.Lease.AcquireTimeoutMilliseconds) * time.Millisecond
	page := config.PageTimeout
	if operation <= 0 || acquire < 0 || page <= 0 {
		return 0, false
	}
	if acquire < operation {
		acquire = operation
	}
	if operation > time.Duration(math.MaxInt64)/2 {
		return 0, false
	}
	budget := operation * 2
	if budget > time.Duration(math.MaxInt64)-acquire {
		return 0, false
	}
	budget += acquire
	if budget > time.Duration(math.MaxInt64)-page {
		return 0, false
	}
	return budget + page, true
}

func permanentBatchError(err error) bool {
	return errors.Is(err, batch.ErrInvalidConfig) || errors.Is(err, batch.ErrDefinitionConflict) ||
		errors.Is(err, batch.ErrInvalidProgress) || errors.Is(err, coordination.ErrInvalidFence) ||
		errors.Is(err, coordination.ErrAuthorityMismatch)
}

func nonRetryableBatchError(err error) error {
	return temporalerror.NewNonRetryableApplicationError(err.Error(), batchPermanentErrorType, err)
}
