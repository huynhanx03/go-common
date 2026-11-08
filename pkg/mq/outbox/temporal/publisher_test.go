package temporaloutbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

func TestPublisherDeliveryContract(t *testing.T) {
	starter := &fakeWorkflowStarter{run: &fakeWorkflowRun{id: "workflow-42", runID: "run-9"}}
	memo := map[string]any{"source": "outbox"}
	routes := map[string]Route{
		"email": {
			Workflow:   "SendEmail",
			TaskQueue:  "email-workers",
			Decode:     func(_ context.Context, message outbox.Message) (any, error) { return string(message.Payload), nil },
			WorkflowID: func(message outbox.Message) (string, error) { return "outbox-" + message.ID, nil },
			Options: client.StartWorkflowOptions{
				ID:                                       "caller-id",
				TaskQueue:                                "caller-queue",
				WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
				WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
				WorkflowExecutionErrorWhenAlreadyStarted: false,
				WorkflowExecutionTimeout:                 30 * time.Second,
				Memo:                                     memo,
			},
		},
	}
	publisher, err := NewPublisher(starter, routes)
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	delete(routes, "email")
	memo["source"] = "caller mutation"

	ctx := logger.WithCorrelationID(context.Background(), "relay-42")
	message := outbox.Message{ID: "message-7", Destination: "email", Payload: []byte("hello")}
	ack, err := publisher.Publish(ctx, message)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if ack.Reference != "outbox-message-7" {
		t.Fatalf("PublishAck.Reference = %q; want stable workflow ID", ack.Reference)
	}
	if len(starter.calls) != 1 {
		t.Fatalf("ExecuteWorkflow calls = %d; want one", len(starter.calls))
	}
	call := starter.calls[0]
	if call.workflow != "SendEmail" || len(call.args) != 1 || call.args[0] != "hello" {
		t.Fatalf("workflow start = %#v; want allowlisted workflow and decoded input", call)
	}
	if call.options.ID != "outbox-message-7" || call.options.TaskQueue != "email-workers" {
		t.Fatalf("workflow ID/queue came from message or caller options: %#v", call.options)
	}
	if call.options.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL ||
		call.options.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE ||
		!call.options.WorkflowExecutionErrorWhenAlreadyStarted {
		t.Fatalf("workflow duplicate protection = %#v", call.options)
	}
	if call.options.WorkflowExecutionTimeout != 30*time.Second || call.options.Memo["source"] != "outbox" {
		t.Fatalf("native route options were not preserved as an owned snapshot: %#v", call.options)
	}
	if logger.CorrelationID(call.ctx) != "relay-42" || logger.RequestID(call.ctx) != "" {
		t.Fatalf("outbox identity was not restored without inventing a request ID: request=%q correlation=%q", logger.RequestID(call.ctx), logger.CorrelationID(call.ctx))
	}
}

func TestPublisherTreatsDuplicateAndAmbiguousStartAsSafeRetry(t *testing.T) {
	starter := &fakeWorkflowStarter{errors: []error{
		&serviceerror.Unavailable{Message: "response lost after server accepted the start"},
		&serviceerror.WorkflowExecutionAlreadyStarted{Message: "stable ID already exists"},
	}, run: &fakeWorkflowRun{id: "stable-workflow"}}
	publisher := newTestPublisher(t, starter)
	message := outbox.Message{ID: "operation-3", Destination: "task", Payload: []byte("input")}

	_, err := publisher.Publish(context.Background(), message)
	var firstFailure *outbox.PublishError
	if !errors.As(err, &firstFailure) || !firstFailure.Retryable || !firstFailure.Ambiguous || !errors.Is(err, starter.errors[0]) {
		t.Fatalf("first publish failure = %#v; want retryable ambiguous upstream error", err)
	}
	ack, err := publisher.Publish(context.Background(), message)
	if err != nil || ack.Reference != "stable-operation-3" {
		t.Fatalf("duplicate publish = %#v, %v; want stable workflow acknowledgment", ack, err)
	}
	if len(starter.calls) != 2 || starter.calls[0].options.ID != starter.calls[1].options.ID {
		t.Fatalf("retry used different workflow IDs: %#v", starter.calls)
	}
}

func TestPublisherRejectsInvalidDeliveryBeforeCallingTemporal(t *testing.T) {
	decodeErr := errors.New("unsupported payload")
	starter := &fakeWorkflowStarter{}
	publisher, err := NewPublisher(starter, map[string]Route{
		"decode-fails": routeForTest(func(context.Context, outbox.Message) (any, error) { return nil, decodeErr }),
		"id-fails": {
			Workflow:   "TaskWorkflow",
			TaskQueue:  "tasks",
			Decode:     func(context.Context, outbox.Message) (any, error) { return "input", nil },
			WorkflowID: func(outbox.Message) (string, error) { return "bad\nworkflow-id", nil },
		},
	})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	for _, test := range []struct {
		name    string
		message outbox.Message
		want    error
	}{
		{name: "unknown route", message: outbox.Message{ID: "m1", Destination: "unknown"}, want: ErrRouteNotConfigured},
		{name: "decoder failure", message: outbox.Message{ID: "m2", Destination: "decode-fails"}, want: ErrInvalidMessage},
		{name: "invalid workflow ID", message: outbox.Message{ID: "m3", Destination: "id-fails"}, want: ErrInvalidMessage},
		{name: "unsafe correlation", message: outbox.Message{ID: "m4", Destination: "id-fails", Metadata: outbox.Metadata{CorrelationID: "bad value"}}, want: ErrInvalidMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := publisher.Publish(context.Background(), test.message)
			var failure *outbox.PublishError
			if !errors.As(err, &failure) || failure.Retryable || !errors.Is(err, test.want) {
				t.Fatalf("Publish() error = %#v; want permanent %v", err, test.want)
			}
		})
	}
	if len(starter.calls) != 0 {
		t.Fatalf("invalid messages reached Temporal: %d starts", len(starter.calls))
	}
}

func TestPublisherDoesNotCallTemporalWhenCanceledBeforeStart(t *testing.T) {
	starter := &fakeWorkflowStarter{}
	publisher := newTestPublisher(t, starter)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := publisher.Publish(ctx, outbox.Message{ID: "m1", Destination: "task"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() error = %v; want context.Canceled", err)
	}
	if len(starter.calls) != 0 {
		t.Fatalf("canceled request invoked Temporal %d times", len(starter.calls))
	}
}

func TestPublisherAcceptsWorkflowIDWithinTemporalLimit(t *testing.T) {
	starter := &fakeWorkflowStarter{run: &fakeWorkflowRun{id: strings.Repeat("w", maximumWorkflowIDBytes)}}
	publisher, err := NewPublisher(starter, map[string]Route{
		"task": {
			Workflow:   "TaskWorkflow",
			TaskQueue:  "tasks",
			Decode:     func(context.Context, outbox.Message) (any, error) { return nil, nil },
			WorkflowID: func(outbox.Message) (string, error) { return strings.Repeat("w", maximumWorkflowIDBytes), nil },
		},
	})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	ack, err := publisher.Publish(context.Background(), outbox.Message{ID: "message-1", Destination: "task"})
	if err != nil {
		t.Fatalf("Publish() rejected a workflow ID within Temporal's limit: %v", err)
	}
	if len(ack.Reference) != maximumWorkflowIDBytes || len(starter.calls) != 1 {
		t.Fatalf("workflow start/ack = %d bytes/%d calls; want ID length %d and one call", len(ack.Reference), len(starter.calls), maximumWorkflowIDBytes)
	}
}

func TestPublisherClassifiesTemporalStartFailures(t *testing.T) {
	tests := []struct {
		name      string
		cause     error
		retryable bool
		ambiguous bool
		code      string
	}{
		{name: "invalid request is permanent", cause: &serviceerror.InvalidArgument{Message: "invalid start options"}, code: CodeWorkflowStartDenied},
		{name: "missing namespace is permanent", cause: &serviceerror.NamespaceNotFound{Message: "namespace missing"}, code: CodeWorkflowStartDenied},
		{name: "permission failure is permanent", cause: &serviceerror.PermissionDenied{Message: "denied"}, code: CodeWorkflowStartDenied},
		{name: "resource exhaustion is ambiguous", cause: &serviceerror.ResourceExhausted{Message: "busy"}, retryable: true, ambiguous: true, code: CodeWorkflowStart},
		{name: "caller timeout is ambiguous", cause: context.DeadlineExceeded, retryable: true, ambiguous: true, code: CodeWorkflowStart},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			starter := &fakeWorkflowStarter{errors: []error{test.cause}}
			publisher := newTestPublisher(t, starter)
			_, err := publisher.Publish(context.Background(), outbox.Message{ID: "operation-1", Destination: "task"})
			var failure *outbox.PublishError
			if !errors.As(err, &failure) || failure.Code != test.code || failure.Retryable != test.retryable || failure.Ambiguous != test.ambiguous {
				t.Fatalf("Publish() failure = %#v; want code %q retryable=%t ambiguous=%t", err, test.code, test.retryable, test.ambiguous)
			}
			if !errors.Is(err, test.cause) {
				t.Fatalf("Publish() error did not retain Temporal cause %v: %v", test.cause, err)
			}
		})
	}
}

func TestNewPublisherRejectsInvalidRouteCatalog(t *testing.T) {
	for _, test := range []struct {
		name    string
		starter WorkflowStarter
		routes  map[string]Route
	}{
		{name: "nil starter", routes: map[string]Route{"task": routeForTest(nil)}},
		{name: "empty routes", starter: &fakeWorkflowStarter{}, routes: map[string]Route{}},
		{name: "missing workflow", starter: &fakeWorkflowStarter{}, routes: map[string]Route{"task": {TaskQueue: "tasks", Decode: func(context.Context, outbox.Message) (any, error) { return nil, nil }, WorkflowID: func(outbox.Message) (string, error) { return "id", nil }}}},
		{name: "missing queue", starter: &fakeWorkflowStarter{}, routes: map[string]Route{"task": {Workflow: "Run", Decode: func(context.Context, outbox.Message) (any, error) { return nil, nil }, WorkflowID: func(outbox.Message) (string, error) { return "id", nil }}}},
		{name: "missing decoder", starter: &fakeWorkflowStarter{}, routes: map[string]Route{"task": {Workflow: "Run", TaskQueue: "tasks", WorkflowID: func(outbox.Message) (string, error) { return "id", nil }}}},
		{name: "missing ID resolver", starter: &fakeWorkflowStarter{}, routes: map[string]Route{"task": {Workflow: "Run", TaskQueue: "tasks", Decode: func(context.Context, outbox.Message) (any, error) { return nil, nil }}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewPublisher(test.starter, test.routes); !errors.Is(err, ErrInvalidRoutes) {
				t.Fatalf("NewPublisher() error = %v; want ErrInvalidRoutes", err)
			}
		})
	}
}

func newTestPublisher(t *testing.T, starter *fakeWorkflowStarter) *Publisher {
	t.Helper()
	publisher, err := NewPublisher(starter, map[string]Route{"task": routeForTest(func(_ context.Context, message outbox.Message) (any, error) {
		return string(message.Payload), nil
	})})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	return publisher
}

func routeForTest(decode func(context.Context, outbox.Message) (any, error)) Route {
	return Route{
		Workflow:   "TaskWorkflow",
		TaskQueue:  "tasks",
		Decode:     decode,
		WorkflowID: func(message outbox.Message) (string, error) { return "stable-" + message.ID, nil },
	}
}

type fakeStart struct {
	ctx      context.Context
	options  client.StartWorkflowOptions
	workflow any
	args     []any
}

type fakeWorkflowStarter struct {
	client.Client
	calls  []fakeStart
	errors []error
	index  int
	run    client.WorkflowRun
}

func (starter *fakeWorkflowStarter) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	starter.calls = append(starter.calls, fakeStart{ctx: ctx, options: options, workflow: workflow, args: args})
	if starter.index < len(starter.errors) {
		err := starter.errors[starter.index]
		starter.index++
		if err != nil {
			return nil, err
		}
	}
	return starter.run, nil
}

type fakeWorkflowRun struct {
	client.WorkflowRun
	id    string
	runID string
}

func (run *fakeWorkflowRun) GetID() string                  { return run.id }
func (run *fakeWorkflowRun) GetRunID() string               { return run.runID }
func (run *fakeWorkflowRun) GetFirstExecutionRunID() string { return run.runID }

var _ WorkflowStarter = (*fakeWorkflowStarter)(nil)
