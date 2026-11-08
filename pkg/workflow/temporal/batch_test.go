package temporal

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	temporalerror "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/workflow/batch"
)

func TestBatchWorkflow(t *testing.T) {
	t.Run("continues after a bounded segment and carries its committed checkpoint hint", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		environment := suite.NewTestWorkflowEnvironment()
		environment.RegisterWorkflowWithOptions(BatchWorkflow, workflow.RegisterOptions{Name: "BatchWorkflow"})
		var calls atomic.Int32
		environment.RegisterActivityWithOptions(func(context.Context, batch.Definition) (batch.Result, error) {
			call := calls.Add(1)
			return batch.Result{Key: "job/a", Version: int64(call), ProcessedCount: int64(call), Done: call == 2}, nil
		}, activity.RegisterOptions{Name: "runBatchSegment"})
		input := validBatchInput()
		environment.ExecuteWorkflow(BatchWorkflow, input)
		err := environment.GetWorkflowError()
		var continueError *workflow.ContinueAsNewError
		if !workflow.IsContinueAsNewError(err) || !errors.As(err, &continueError) {
			t.Fatalf("workflow error = %v; want Continue-As-New after one nonterminal segment", err)
		}
		var continued BatchWorkflowInput
		if err := converter.GetDefaultDataConverter().FromPayloads(continueError.Input, &continued); err != nil {
			t.Fatalf("decode Continue-As-New input: %v", err)
		}
		wantLast := batch.Result{Key: "job/a", Version: 1, ProcessedCount: 1}
		if !sameTemporalDefinition(continued.Definition, input.Definition) || continued.ActivityName != input.ActivityName || continued.ActivityOptions.HeartbeatTimeout != input.ActivityOptions.HeartbeatTimeout || continued.ContinueAfterSegments != 1 || continued.LastResult != wantLast || calls.Load() != 1 {
			t.Fatalf("continued input/calls = %#v/%d; want same definition/options and last committed result", continued, calls.Load())
		}
	})

	t.Run("retries a transient Activity failure using project supplied policy", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		environment := suite.NewTestWorkflowEnvironment()
		environment.RegisterWorkflowWithOptions(BatchWorkflow, workflow.RegisterOptions{Name: "BatchWorkflow"})
		var calls atomic.Int32
		environment.RegisterActivityWithOptions(func(context.Context, batch.Definition) (batch.Result, error) {
			if calls.Add(1) == 1 {
				return batch.Result{}, errors.New("temporary database error")
			}
			return batch.Result{Key: "job/a", Version: 1, ProcessedCount: 1, Done: true}, nil
		}, activity.RegisterOptions{Name: "runBatchSegment"})
		input := validBatchInput()
		input.ActivityOptions.StartToCloseTimeout = 0
		input.ActivityOptions.ScheduleToCloseTimeout = time.Minute
		input.ActivityOptions.RetryPolicy = &temporalerror.RetryPolicy{MaximumAttempts: 2, InitialInterval: time.Millisecond}
		environment.ExecuteWorkflow(BatchWorkflow, input)
		if err := environment.GetWorkflowError(); err != nil {
			t.Fatalf("BatchWorkflow() error = %v", err)
		}
		if calls.Load() != 2 {
			t.Fatalf("Activity attempts = %d; want one retry", calls.Load())
		}
	})

	t.Run("rejects incomplete or unstable Activity configuration before scheduling", func(t *testing.T) {
		cases := []struct {
			name   string
			change func(*BatchWorkflowInput)
		}{
			{name: "missing activity name", change: func(input *BatchWorkflowInput) { input.ActivityName = "" }},
			{name: "missing activity deadline", change: func(input *BatchWorkflowInput) { input.ActivityOptions.StartToCloseTimeout = 0 }},
			{name: "missing heartbeat timeout", change: func(input *BatchWorkflowInput) { input.ActivityOptions.HeartbeatTimeout = 0 }},
			{name: "fixed activity ID", change: func(input *BatchWorkflowInput) { input.ActivityOptions.ActivityID = "same-id" }},
			{name: "missing segment bound", change: func(input *BatchWorkflowInput) { input.ContinueAfterSegments = 0 }},
			{name: "different carried key", change: func(input *BatchWorkflowInput) { input.LastResult = batch.Result{Key: "other"} }},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				var suite testsuite.WorkflowTestSuite
				environment := suite.NewTestWorkflowEnvironment()
				environment.RegisterWorkflowWithOptions(BatchWorkflow, workflow.RegisterOptions{Name: "BatchWorkflow"})
				var activityCalls atomic.Int32
				environment.RegisterActivityWithOptions(func(context.Context, batch.Definition) (batch.Result, error) {
					activityCalls.Add(1)
					return batch.Result{}, nil
				}, activity.RegisterOptions{Name: "runBatchSegment"})
				input := validBatchInput()
				test.change(&input)
				environment.ExecuteWorkflow(BatchWorkflow, input)
				var appErr *temporalerror.ApplicationError
				if !errors.As(environment.GetWorkflowError(), &appErr) || !appErr.NonRetryable() {
					t.Fatalf("workflow error = %v; want non-retryable input error", environment.GetWorkflowError())
				}
				if activityCalls.Load() != 0 {
					t.Fatalf("invalid workflow scheduled %d Activities", activityCalls.Load())
				}
			})
		}
	})
}

func TestBatchActivityRecovery(t *testing.T) {
	store := newTemporalMemoryStore()
	definition := temporalBatchDefinition("activity-recovery")
	var stepCalls atomic.Int32
	runner := newTemporalRunner(t, store)
	activities := NewBatchActivities(runner, func(_ context.Context, current batch.Checkpoint) (batch.Progress, error) {
		stepCalls.Add(1)
		return batch.Progress{Cursor: []byte{1}, Processed: 1, Done: true}, nil
	})

	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterWorkflowWithOptions(runBatchActivityWorkflow, workflow.RegisterOptions{Name: "runBatchActivityWorkflow"})
	var attempts atomic.Int32
	environment.RegisterActivityWithOptions(func(ctx context.Context, input batch.Definition) (batch.Result, error) {
		activity.RecordHeartbeat(ctx, batchHeartbeat{Key: input.Key, Version: 99, ProcessedCount: 99})
		result, err := activities.Run(ctx, input)
		if err == nil && attempts.Add(1) == 1 {
			return batch.Result{}, errors.New("worker lost the Activity completion response")
		}
		return result, err
	}, activity.RegisterOptions{Name: "runBatchActivity"})
	options := activityTestOptions()
	environment.ExecuteWorkflow(runBatchActivityWorkflow, definition, options)
	if err := environment.GetWorkflowError(); err != nil {
		t.Fatalf("Activity retry failed: %v", err)
	}
	var result batch.Result
	if err := environment.GetWorkflowResult(&result); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.checkpointSnapshot()
	if result.Version != 1 || result.ProcessedCount != 1 || !result.Done || checkpoint.Version != 1 || stepCalls.Load() != 1 || store.openCount() != 2 {
		t.Fatalf("retry trusted stale heartbeat or repeated work: result=%#v checkpoint=%#v steps=%d opens=%d", result, checkpoint, stepCalls.Load(), store.openCount())
	}
}

func TestBatchActivityRejectsInsufficientHeartbeatBudget(t *testing.T) {
	store := newTemporalMemoryStore()
	runner := newTemporalRunner(t, store)
	activities := NewBatchActivities(runner, func(context.Context, batch.Checkpoint) (batch.Progress, error) {
		t.Fatal("invalid heartbeat budget reached the business callback")
		return batch.Progress{}, nil
	})
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterWorkflowWithOptions(runBatchActivityWorkflow, workflow.RegisterOptions{Name: "runBatchActivityWorkflow"})
	environment.RegisterActivityWithOptions(activities.Run, activity.RegisterOptions{Name: "runBatchActivity"})
	options := activityTestOptions()
	options.HeartbeatTimeout = 10 * time.Millisecond
	environment.ExecuteWorkflow(runBatchActivityWorkflow, temporalBatchDefinition("short-heartbeat"), options)
	var appErr *temporalerror.ApplicationError
	if !errors.As(environment.GetWorkflowError(), &appErr) || !appErr.NonRetryable() {
		t.Fatalf("workflow error = %v; want non-retryable heartbeat budget error", environment.GetWorkflowError())
	}
	if store.openCount() != 0 {
		t.Fatalf("checkpoint opened %d times before heartbeat validation", store.openCount())
	}
}

func TestBatchActivityDoesNotRetryPermanentDefinitionConflict(t *testing.T) {
	for _, scenario := range []string{"definition conflict", "invalid progress"} {
		t.Run(scenario, func(t *testing.T) {
			store := newTemporalMemoryStore()
			definition := temporalBatchDefinition("permanent-" + scenario)
			if scenario == "definition conflict" {
				if _, err := store.Open(context.Background(), definition); err != nil {
					t.Fatal(err)
				}
			}
			runner := newTemporalRunner(t, store)
			activities := NewBatchActivities(runner, func(_ context.Context, checkpoint batch.Checkpoint) (batch.Progress, error) {
				if scenario == "definition conflict" {
					t.Fatal("definition conflict reached the page callback")
				}
				return batch.Progress{Cursor: checkpoint.Cursor, Processed: -1}, nil
			})
			var suite testsuite.WorkflowTestSuite
			environment := suite.NewTestWorkflowEnvironment()
			environment.RegisterWorkflowWithOptions(runBatchActivityWorkflow, workflow.RegisterOptions{Name: "runBatchActivityWorkflow"})
			var attempts atomic.Int32
			environment.RegisterActivityWithOptions(func(ctx context.Context, input batch.Definition) (batch.Result, error) {
				attempts.Add(1)
				return activities.Run(ctx, input)
			}, activity.RegisterOptions{Name: "runBatchActivity"})
			if scenario == "definition conflict" {
				definition.InputFingerprint = "changed"
			}
			options := activityTestOptions()
			options.RetryPolicy = &temporalerror.RetryPolicy{MaximumAttempts: 3, InitialInterval: time.Millisecond}
			environment.ExecuteWorkflow(runBatchActivityWorkflow, definition, options)
			var appErr *temporalerror.ApplicationError
			if !errors.As(environment.GetWorkflowError(), &appErr) || !appErr.NonRetryable() {
				t.Fatalf("workflow error = %v; want non-retryable permanent batch error", environment.GetWorkflowError())
			}
			if attempts.Load() != 1 {
				t.Fatalf("permanent failure attempted %d times; want one Activity attempt", attempts.Load())
			}
			checkpoint := store.checkpointSnapshot()
			if checkpoint.Version != 0 || checkpoint.ProcessedCount != 0 || checkpoint.Status != batch.Ready {
				t.Fatalf("permanent failure changed checkpoint: %#v", checkpoint)
			}
		})
	}
}

func validBatchInput() BatchWorkflowInput {
	return BatchWorkflowInput{
		Definition:            temporalBatchDefinition("job/a"),
		ActivityName:          "runBatchSegment",
		ActivityOptions:       workflow.ActivityOptions{StartToCloseTimeout: time.Minute, HeartbeatTimeout: 30 * time.Second},
		ContinueAfterSegments: 1,
	}
}

func runBatchActivityWorkflow(ctx workflow.Context, definition batch.Definition, options workflow.ActivityOptions) (batch.Result, error) {
	ctx = workflow.WithActivityOptions(ctx, options)
	var result batch.Result
	err := workflow.ExecuteActivity(ctx, "runBatchActivity", definition).Get(ctx, &result)
	return result, err
}

func activityTestOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: time.Second,
		HeartbeatTimeout:    time.Second,
		RetryPolicy:         &temporalerror.RetryPolicy{MaximumAttempts: 2, InitialInterval: time.Millisecond},
	}
}

func newTemporalRunner(t *testing.T, store *temporalMemoryStore) *batch.Runner {
	t.Helper()
	runner, err := batch.New(store, logger.Wrap(zap.NewNop()), batch.Config{
		Lease:       settings.Coordination{TTLMilliseconds: 900, RenewIntervalMilliseconds: 300, OperationTimeoutMilliseconds: 100, CleanupTimeoutMilliseconds: 100},
		PageTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func temporalBatchDefinition(key string) batch.Definition {
	return batch.Definition{Key: key, InputFingerprint: "input-v1", CursorSchema: "cursor-v1", Boundary: []byte("cutoff")}
}

func sameTemporalDefinition(first, second batch.Definition) bool {
	return first.Key == second.Key && first.InputFingerprint == second.InputFingerprint && first.CursorSchema == second.CursorSchema && bytes.Equal(first.Boundary, second.Boundary)
}

type temporalMemoryStore struct {
	mu          sync.Mutex
	checkpoint  batch.Checkpoint
	initialized bool
	owner       string
	fence       int64
	leaseUntil  time.Time
	openCalls   int
	applyCalls  int
}

func newTemporalMemoryStore() *temporalMemoryStore { return &temporalMemoryStore{} }

func (s *temporalMemoryStore) Open(_ context.Context, definition batch.Definition) (batch.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openCalls++
	if s.initialized {
		if !sameTemporalDefinition(s.checkpoint.Definition, definition) {
			return batch.Checkpoint{}, batch.ErrDefinitionConflict
		}
		return cloneTemporalCheckpoint(s.checkpoint), nil
	}
	s.initialized = true
	s.checkpoint = batch.Checkpoint{
		Definition: cloneTemporalDefinition(definition),
		Status:     batch.Ready,
		Fence:      coordination.FenceToken{Authority: "test-authority"},
	}
	return cloneTemporalCheckpoint(s.checkpoint), nil
}

func (s *temporalMemoryStore) Get(_ context.Context, key string) (batch.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized || key != s.checkpoint.Definition.Key {
		return batch.Checkpoint{}, errors.New("checkpoint missing")
	}
	return cloneTemporalCheckpoint(s.checkpoint), nil
}

func (s *temporalMemoryStore) TryAcquireFenced(_ context.Context, request coordination.AcquireRequest) (coordination.FencedLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Resource != s.checkpoint.Definition.Key || request.TTL <= 0 {
		return nil, coordination.ErrInvalidConfig
	}
	if s.checkpoint.Status == batch.Completed {
		return nil, batch.ErrCompleted
	}
	if s.owner != "" && s.leaseUntil.After(time.Now()) {
		return nil, coordination.ErrHeld
	}
	s.fence++
	s.owner = request.Owner
	s.leaseUntil = time.Now().Add(request.TTL)
	s.checkpoint.Owner = s.owner
	s.checkpoint.Status = batch.Running
	s.checkpoint.Fence = coordination.FenceToken{Authority: "test-authority", Value: s.fence}
	s.checkpoint.LeaseUntil = s.leaseUntil
	return &temporalMemoryLease{store: s, grant: coordination.Grant{
		Resource: request.Resource,
		Owner:    request.Owner,
		Fence:    s.checkpoint.Fence,
	}, ttl: request.TTL}, nil
}

func (s *temporalMemoryStore) Apply(_ context.Context, request batch.StepRequest, step batch.Step) (batch.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateGrant(request.Grant); err != nil {
		return batch.Checkpoint{}, err
	}
	if s.checkpoint.LastStepID == request.StepID && s.checkpoint.Version == request.ExpectedVersion+1 {
		return cloneTemporalCheckpoint(s.checkpoint), nil
	}
	if s.checkpoint.Version != request.ExpectedVersion || s.checkpoint.Status == batch.Completed {
		return batch.Checkpoint{}, batch.ErrConflict
	}
	progress, err := step(context.Background(), cloneTemporalCheckpoint(s.checkpoint))
	if err != nil {
		return batch.Checkpoint{}, err
	}
	s.checkpoint.Cursor = append([]byte(nil), progress.Cursor...)
	s.checkpoint.Version++
	s.checkpoint.ProcessedCount += progress.Processed
	s.checkpoint.LastStepID = request.StepID
	if progress.Done {
		s.checkpoint.Status = batch.Completed
	}
	s.applyCalls++
	return cloneTemporalCheckpoint(s.checkpoint), nil
}

func (s *temporalMemoryStore) validateGrant(grant coordination.Grant) error {
	if grant.Resource != s.checkpoint.Definition.Key || grant.Owner != s.owner || grant.Owner == "" || grant.Fence != s.checkpoint.Fence || !s.leaseUntil.After(time.Now()) {
		return coordination.ErrLeaseLost
	}
	return nil
}

func (s *temporalMemoryStore) openCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openCalls
}

func (s *temporalMemoryStore) checkpointSnapshot() batch.Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTemporalCheckpoint(s.checkpoint)
}

type temporalMemoryLease struct {
	store *temporalMemoryStore
	grant coordination.Grant
	ttl   time.Duration
}

func (l *temporalMemoryLease) Grant() coordination.Grant { return l.grant }

func (l *temporalMemoryLease) Renew(context.Context) error {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.store.validateGrant(l.grant); err != nil {
		return err
	}
	l.store.leaseUntil = time.Now().Add(l.ttl)
	return nil
}

func (l *temporalMemoryLease) Release(context.Context) error {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.store.validateGrant(l.grant); err != nil {
		return err
	}
	l.store.owner = ""
	l.store.checkpoint.Owner = ""
	if l.store.checkpoint.Status != batch.Completed {
		l.store.checkpoint.Status = batch.Ready
	}
	return nil
}

func cloneTemporalDefinition(definition batch.Definition) batch.Definition {
	definition.Boundary = append([]byte(nil), definition.Boundary...)
	return definition
}

func cloneTemporalCheckpoint(checkpoint batch.Checkpoint) batch.Checkpoint {
	checkpoint.Definition = cloneTemporalDefinition(checkpoint.Definition)
	checkpoint.Cursor = append([]byte(nil), checkpoint.Cursor...)
	return checkpoint
}
