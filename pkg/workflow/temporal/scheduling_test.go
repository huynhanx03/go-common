package temporal

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestDurableWakeupHonorsRevisionedRescheduleAndConditionalClaim(t *testing.T) {
	state := &testOperation{revision: 1}
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterWorkflowWithOptions(revisionedWakeupWorkflow, workflow.RegisterOptions{Name: "revisionedWakeupWorkflow"})
	environment.RegisterActivityWithOptions(func(_ context.Context, revision uint64) error {
		state.claim(revision)
		return nil
	}, activity.RegisterOptions{Name: "claimOperation"})

	environment.RegisterDelayedCallback(func() {
		// A duplicate/stale delivery must not replace the current schedule.
		environment.SignalWorkflow("operation", wakeupCommand{Revision: 1, Delay: time.Minute})
	}, 10*time.Minute)
	environment.RegisterDelayedCallback(func() {
		state.set(2, false)
		environment.SignalWorkflow("operation", wakeupCommand{Revision: 2, Delay: 2 * time.Hour})
	}, 20*time.Minute)
	environment.RegisterDelayedCallback(func() {
		if attempts, _ := state.results(); attempts != 0 {
			t.Errorf("operation ran at its superseded deadline: attempts=%d", attempts)
		}
	}, time.Hour)
	environment.RegisterDelayedCallback(func() {
		if attempts, _ := state.results(); attempts != 0 {
			t.Errorf("operation ran before its revised deadline: attempts=%d", attempts)
		}
	}, 2*time.Hour+19*time.Minute)
	environment.ExecuteWorkflow(revisionedWakeupWorkflow, uint64(1), time.Hour)
	if err := environment.GetWorkflowError(); err != nil {
		t.Fatalf("revisioned wakeup workflow failed: %v", err)
	}
	if attempts, claims := state.results(); attempts != 1 || len(claims) != 1 || claims[0] != 2 {
		t.Fatalf("conditional claims = attempts %d, revisions %v; want one current revision 2", attempts, claims)
	}
}

func TestTimerDeliveryCannotClaimAStaleOrCanceledOperation(t *testing.T) {
	for _, test := range []struct {
		name     string
		revision uint64
		canceled bool
	}{
		{name: "newer revision", revision: 2},
		{name: "canceled operation", revision: 1, canceled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &testOperation{revision: 1}
			var suite testsuite.WorkflowTestSuite
			environment := suite.NewTestWorkflowEnvironment()
			environment.RegisterWorkflowWithOptions(revisionedWakeupWorkflow, workflow.RegisterOptions{Name: "revisionedWakeupWorkflow"})
			environment.RegisterActivityWithOptions(func(_ context.Context, revision uint64) error {
				state.claim(revision)
				return nil
			}, activity.RegisterOptions{Name: "claimOperation"})
			environment.RegisterDelayedCallback(func() {
				state.set(test.revision, test.canceled)
				// The due timer and the durable cancellation/reschedule message become
				// visible at the same virtual instant. Either may reach the workflow
				// first; the Activity's conditional claim remains the final guard.
				environment.SignalWorkflow("operation", wakeupCommand{
					Revision: test.revision + 1,
					Cancel:   test.canceled,
					Delay:    time.Hour,
				})
			}, time.Hour)
			environment.ExecuteWorkflow(revisionedWakeupWorkflow, uint64(1), time.Hour)
			if err := environment.GetWorkflowError(); err != nil {
				t.Fatalf("revisioned wakeup workflow failed: %v", err)
			}
			if attempts, claims := state.results(); len(claims) != 0 {
				t.Fatalf("stale timer claimed revisions %v after %d attempts", claims, attempts)
			}
		})
	}
}

type wakeupCommand struct {
	Revision uint64
	Delay    time.Duration
	Cancel   bool
}

func revisionedWakeupWorkflow(ctx workflow.Context, revision uint64, delay time.Duration) error {
	deadline := workflow.Now(ctx).Add(delay)
	commands := workflow.GetSignalChannel(ctx, "operation")
	for {
		remaining := deadline.Sub(workflow.Now(ctx))
		if remaining < 0 {
			remaining = 0
		}
		timerContext, cancelTimer := workflow.WithCancel(ctx)
		timer := workflow.NewTimer(timerContext, remaining)
		var command wakeupCommand
		commandReceived := false
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(commands, func(channel workflow.ReceiveChannel, _ bool) {
			channel.Receive(ctx, &command)
			commandReceived = true
		})
		selector.AddFuture(timer, func(workflow.Future) {})
		selector.Select(ctx)
		cancelTimer()

		if commandReceived {
			if command.Revision <= revision {
				continue
			}
			revision = command.Revision
			if command.Cancel {
				return nil
			}
			deadline = workflow.Now(ctx).Add(command.Delay)
			continue
		}

		activityContext := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		err := workflow.ExecuteActivity(activityContext, "claimOperation", revision).Get(activityContext, nil)
		var racedCommand wakeupCommand
		commands.ReceiveAsync(&racedCommand)
		return err
	}
}

type testOperation struct {
	mu       sync.Mutex
	revision uint64
	canceled bool
	attempts int
	claims   []uint64
}

func (operation *testOperation) set(revision uint64, canceled bool) {
	operation.mu.Lock()
	operation.revision = revision
	operation.canceled = canceled
	operation.mu.Unlock()
}

func (operation *testOperation) claim(revision uint64) {
	operation.mu.Lock()
	defer operation.mu.Unlock()
	operation.attempts++
	if operation.revision == revision && !operation.canceled {
		operation.claims = append(operation.claims, revision)
	}
}

func (operation *testOperation) results() (int, []uint64) {
	operation.mu.Lock()
	defer operation.mu.Unlock()
	return operation.attempts, append([]uint64(nil), operation.claims...)
}
