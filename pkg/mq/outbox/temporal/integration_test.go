//go:build integration

package temporaloutbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/mq/outbox"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	commontemporal "github.com/huynhanx03/go-common/pkg/workflow/temporal"
)

func TestRelayAndTemporalPublisherDeliverySemantics(t *testing.T) {
	server := containers.StartTemporal(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	commonLogger := logger.Wrap(zap.NewNop())
	temporalClient, err := commontemporal.Dial(ctx, commonLogger, settings.Temporal{
		Address: server.Address(), Namespace: "default", DialTimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	defer temporalClient.Close()

	var activityRuns atomic.Int32
	instance, err := commontemporal.NewWorker(temporalClient, settings.TemporalWorker{
		Name: "outbox-integration-worker", TaskQueue: "outbox-integration-tasks",
	}, worker.Options{})
	if err != nil {
		t.Fatalf("NewWorker() = %v", err)
	}
	instance.SDK().RegisterWorkflowWithOptions(outboxWorkflow, workflow.RegisterOptions{Name: "outboxWorkflow"})
	instance.SDK().RegisterActivityWithOptions(func(ctx context.Context, _ string) error {
		activityRuns.Add(1)
		commonLogger.Info(ctx, "outbox workflow activity completed")
		return nil
	}, activity.RegisterOptions{Name: "outboxActivity"})
	workerContext, stopWorker := context.WithCancel(context.Background())
	workerResult := make(chan error, 1)
	go func() { workerResult <- instance.Run(workerContext) }()
	t.Cleanup(func() {
		stopWorker()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := instance.Shutdown(shutdownContext); err != nil {
			t.Errorf("stop outbox integration worker: %v", err)
		}
		select {
		case err := <-workerResult:
			if err != nil {
				t.Errorf("outbox integration worker Run(): %v", err)
			}
		case <-shutdownContext.Done():
			t.Errorf("outbox integration worker did not stop: %v", shutdownContext.Err())
		}
	})

	t.Run("duplicate delivery after lost outbox acknowledgment starts one operation", func(t *testing.T) {
		store := newTemporalTestStore("duplicate-1", "accepted", true)
		starter := &countingWorkflowStarter{Client: temporalClient.SDK()}
		publisher := newIntegrationPublisher(t, starter)
		runTemporalRelay(t, ctx, commonLogger, store, publisher)
		assertWorkflowCompleted(t, ctx, temporalClient.SDK(), "outbox-duplicate-1")
		if store.claims != 2 || store.published != 2 || store.retries != 0 {
			t.Fatalf("relay transitions = claims %d, published %d, retries %d; want 2/2/0", store.claims, store.published, store.retries)
		}
		if starter.starts.Load() != 2 || activityRuns.Load() != 1 {
			t.Fatalf("Temporal starts/activity executions = %d/%d; want duplicate start and one execution", starter.starts.Load(), activityRuns.Load())
		}
	})

	t.Run("transient start failure is retried with the same workflow ID", func(t *testing.T) {
		store := newTemporalTestStore("retry-1", "accepted", false)
		starter := &failOnceWorkflowStarter{Client: temporalClient.SDK()}
		publisher := newIntegrationPublisher(t, starter)
		before := activityRuns.Load()
		runTemporalRelay(t, ctx, commonLogger, store, publisher)
		assertWorkflowCompleted(t, ctx, temporalClient.SDK(), "outbox-retry-1")
		if store.claims != 2 || store.retries != 1 || store.published != 1 {
			t.Fatalf("relay transitions = claims %d, retries %d, published %d; want 2/1/1", store.claims, store.retries, store.published)
		}
		if starter.starts.Load() != 2 || starter.ids[0] != "outbox-retry-1" || starter.ids[1] != starter.ids[0] {
			t.Fatalf("workflow start IDs = %v; want same stable ID on retry", starter.ids)
		}
		if activityRuns.Load()-before != 1 {
			t.Fatalf("Activity execution count = %d; want one", activityRuns.Load()-before)
		}
	})

	t.Run("invalid payload is terminal", func(t *testing.T) {
		store := newTemporalTestStore("invalid-1", "invalid", false)
		starter := &countingWorkflowStarter{Client: temporalClient.SDK()}
		publisher := newIntegrationPublisher(t, starter)
		runTemporalRelay(t, ctx, commonLogger, store, publisher)
		if store.claims != 1 || store.dead != 1 || store.published != 0 || store.retries != 0 {
			t.Fatalf("relay transitions = claims %d, dead %d, published %d, retries %d; want 1/1/0/0", store.claims, store.dead, store.published, store.retries)
		}
		if starter.starts.Load() != 0 {
			t.Fatalf("invalid payload started %d workflows", starter.starts.Load())
		}
	})
}

func newIntegrationPublisher(t *testing.T, starter WorkflowStarter) *Publisher {
	t.Helper()
	publisher, err := NewPublisher(starter, map[string]Route{
		"job": {
			Workflow:  "outboxWorkflow",
			TaskQueue: "outbox-integration-tasks",
			Decode: func(_ context.Context, message outbox.Message) (any, error) {
				if string(message.Payload) != "accepted" {
					return nil, errors.New("invalid test payload")
				}
				return message.ID, nil
			},
			WorkflowID: func(message outbox.Message) (string, error) { return "outbox-" + message.ID, nil },
		},
	})
	if err != nil {
		t.Fatalf("NewPublisher() = %v", err)
	}
	return publisher
}

func runTemporalRelay(t *testing.T, parent context.Context, log *logger.Logger, store *temporalTestStore, publisher outbox.Publisher) {
	t.Helper()
	options := outbox.Options{
		Name:            "temporal-integration",
		Owner:           "integration-worker",
		Concurrency:     1,
		ClaimLimit:      1,
		Destinations:    []string{"job"},
		MaxMessageBytes: 1024,
		LeaseDuration:   5 * time.Second,
		StoreTimeout:    time.Second,
		PublishTimeout:  time.Second,
		FinalizeTimeout: time.Second,
		PollInterval:    time.Millisecond,
		Backoff:         outbox.BackoffFunc(func(uint32) time.Duration { return 0 }),
	}
	relay, err := outbox.New(log, store, publisher, nil, options)
	if err != nil {
		t.Fatalf("outbox.New() = %v", err)
	}
	ctx, cancel := context.WithCancel(parent)
	result := make(chan error, 1)
	go func() { result <- relay.Run(ctx) }()
	select {
	case <-store.terminal:
	case <-parent.Done():
		t.Fatalf("relay did not finalize message: %v", parent.Err())
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("relay.Run() = %v", err)
		}
	case <-parent.Done():
		t.Fatalf("relay did not stop: %v", parent.Err())
	}
}

func assertWorkflowCompleted(t *testing.T, ctx context.Context, sdk client.Client, workflowID string) {
	t.Helper()
	if err := sdk.GetWorkflow(ctx, workflowID, "").Get(ctx, nil); err != nil {
		t.Fatalf("workflow %q did not complete: %v", workflowID, err)
	}
}

func outboxWorkflow(ctx workflow.Context, operationID string) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	return workflow.ExecuteActivity(ctx, "outboxActivity", operationID).Get(ctx, nil)
}

type temporalTestStore struct {
	mu                 sync.Mutex
	message            outbox.Message
	claims             int
	published          int
	retries            int
	dead               int
	finalized          bool
	failFirstPublished bool
	terminal           chan struct{}
	terminalOnce       sync.Once
}

func newTemporalTestStore(id, payload string, failFirstPublished bool) *temporalTestStore {
	return &temporalTestStore{
		message:            outbox.Message{ID: id, Destination: "job", Payload: []byte(payload)},
		failFirstPublished: failFirstPublished,
		terminal:           make(chan struct{}),
	}
}

func (store *temporalTestStore) Claim(_ context.Context, request outbox.ClaimRequest) ([]outbox.ClaimedMessage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if request.Limit < 1 || len(request.Destinations) != 1 || request.Destinations[0] != store.message.Destination {
		return nil, errors.New("unexpected integration claim request")
	}
	if store.finalized || store.dead > 0 {
		return nil, nil
	}
	store.claims++
	attempt := uint32(store.claims)
	return []outbox.ClaimedMessage{{
		Lease:   outbox.Lease{MessageID: store.message.ID, Token: fmt.Sprintf("lease-%d", attempt), Attempt: attempt},
		Message: store.message,
	}}, nil
}

func (store *temporalTestStore) MarkPublished(_ context.Context, _ outbox.Lease, _ outbox.PublishAck) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.published++
	if store.failFirstPublished && store.published == 1 {
		return errors.New("simulated lost outbox acknowledgment")
	}
	store.finalized = true
	store.terminalOnce.Do(func() { close(store.terminal) })
	return nil
}

func (store *temporalTestStore) MarkRetry(_ context.Context, _ outbox.Lease, _ outbox.RetryDisposition) error {
	store.mu.Lock()
	store.retries++
	store.mu.Unlock()
	return nil
}

func (store *temporalTestStore) MarkDead(_ context.Context, _ outbox.Lease, _ outbox.DeadDisposition) error {
	store.mu.Lock()
	store.dead++
	store.mu.Unlock()
	store.terminalOnce.Do(func() { close(store.terminal) })
	return nil
}

type countingWorkflowStarter struct {
	client.Client
	starts atomic.Int32
	mu     sync.Mutex
	ids    []string
}

func (starter *countingWorkflowStarter) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	starter.starts.Add(1)
	starter.mu.Lock()
	starter.ids = append(starter.ids, options.ID)
	starter.mu.Unlock()
	return starter.Client.ExecuteWorkflow(ctx, options, workflow, args...)
}

type failOnceWorkflowStarter struct {
	countingWorkflowStarter
	failed atomic.Bool
}

func (starter *failOnceWorkflowStarter) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	starter.countingWorkflowStarter.starts.Add(1)
	starter.countingWorkflowStarter.mu.Lock()
	starter.countingWorkflowStarter.ids = append(starter.countingWorkflowStarter.ids, options.ID)
	starter.countingWorkflowStarter.mu.Unlock()
	if starter.failed.CompareAndSwap(false, true) {
		return nil, &serviceerror.Unavailable{Message: "temporary Temporal outage"}
	}
	return starter.Client.ExecuteWorkflow(ctx, options, workflow, args...)
}

var _ outbox.Store = (*temporalTestStore)(nil)
var _ WorkflowStarter = (*countingWorkflowStarter)(nil)
var _ WorkflowStarter = (*failOnceWorkflowStarter)(nil)
