package temporal

import (
	"context"
	"strings"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/logger"
)

func TestWorkflowActivityObservability(t *testing.T) {
	tests := []struct {
		name            string
		requestID       string
		correlationID   string
		wantRequestID   string
		wantCorrelation string
	}{
		{name: "request and explicit correlation", requestID: "request-42", correlationID: "job-9", wantRequestID: "request-42", wantCorrelation: "job-9"},
		{name: "request supplies correlation fallback", requestID: "request-42", wantRequestID: "request-42", wantCorrelation: "request-42"},
		{name: "valid peer survives invalid request", requestID: "bad/request", correlationID: "job-9", wantCorrelation: "job-9"},
		{name: "valid peer survives oversized request", requestID: strings.Repeat("r", maxPropagatedValueBytes+1), correlationID: "job-9", wantCorrelation: "job-9"},
		{name: "valid request survives malformed correlation", requestID: "request-42", correlationID: "bad value", wantRequestID: "request-42", wantCorrelation: "request-42"},
		{name: "absent identity stays absent"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, observed := observer.New(zap.DebugLevel)
			commonLogger := logger.Wrap(zap.New(base))
			var suite testsuite.WorkflowTestSuite
			suite.SetLogger(newSDKLogger(commonLogger))
			environment := suite.NewTestWorkflowEnvironment()
			environment.SetContextPropagators([]workflow.ContextPropagator{&contextPropagator{}})
			environment.SetHeader(identityHeader(t, test.requestID, test.correlationID))
			environment.SetWorkerOptions(worker.Options{
				Interceptors: []interceptor.WorkerInterceptor{&observabilityInterceptor{}},
			})
			environment.RegisterActivityWithOptions(func(ctx context.Context) error {
				commonLogger.Info(ctx, "activity completed")
				return nil
			}, activity.RegisterOptions{Name: observabilityActivityName})
			environment.RegisterWorkflowWithOptions(observabilityChildWorkflow, workflow.RegisterOptions{Name: "observability-child"})
			environment.RegisterDelayedCallback(func() {
				environment.UpdateWorkflow("run-activity", "update-1", &testsuite.TestUpdateCallback{
					OnComplete: func(_ any, err error) {
						if err != nil {
							t.Errorf("workflow update failed: %v", err)
						}
						environment.SignalWorkflow("continue", "resume")
					},
				})
			}, time.Second)
			environment.ExecuteWorkflow(observabilityWorkflow)
			if err := environment.GetWorkflowError(); err != nil {
				t.Fatalf("workflow execution failed: %v", err)
			}

			entries := observed.FilterMessage("activity completed").All()
			if len(entries) != 4 {
				t.Fatalf("activity log count = %d; want parent, child, update, and signal activity logs", len(entries))
			}
			for _, entry := range entries {
				fields := entry.ContextMap()
				if stringField(fields, "request_id") != test.wantRequestID || stringField(fields, "correlation_id") != test.wantCorrelation {
					t.Errorf("activity identity fields = %#v; want request=%q correlation=%q", fields, test.wantRequestID, test.wantCorrelation)
				}
				for _, key := range []string{"workflow_id", "run_id", "activity_id", "attempt"} {
					if fields[key] == "" {
						t.Errorf("activity identity field %q is missing: %#v", key, fields)
					}
				}
			}

			workflowEntries := observed.FilterMessage("workflow started").All()
			if len(workflowEntries) != 1 {
				t.Fatalf("workflow log count = %d; want one replay-aware workflow log", len(workflowEntries))
			}
			workflowFields := workflowEntries[0].ContextMap()
			if stringField(workflowFields, "request_id") != test.wantRequestID || stringField(workflowFields, "correlation_id") != test.wantCorrelation {
				t.Errorf("workflow identity fields = %#v; want request=%q correlation=%q", workflowFields, test.wantRequestID, test.wantCorrelation)
			}
			if workflowFields["workflow_id"] == "" || workflowFields["run_id"] == "" {
				t.Errorf("workflow identity fields are incomplete: %#v", workflowFields)
			}

			childEntries := observed.FilterMessage("child workflow started").All()
			if len(childEntries) != 1 || stringField(childEntries[0].ContextMap(), "request_id") != test.wantRequestID {
				t.Errorf("child workflow did not inherit propagated identity: %#v", childEntries)
			}
		})
	}
}

func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

func TestContextPropagatorRestoresIdentityInBothContexts(t *testing.T) {
	propagator := &contextPropagator{}
	writer := identityHeaderWriter{header: make(map[string]*commonpb.Payload)}
	ctx := logger.WithRequestID(context.Background(), "request-42")
	ctx = logger.WithCorrelationID(ctx, "job-9")
	if err := propagator.Inject(ctx, writer); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	got, err := propagator.Extract(context.Background(), identityHeaderReader{header: writer.header})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if logger.RequestID(got) != "request-42" || logger.CorrelationID(got) != "job-9" {
		t.Fatalf("ordinary context identity = request %q, correlation %q", logger.RequestID(got), logger.CorrelationID(got))
	}

	// Workflow-context propagation is exercised through the SDK test environment above;
	// this assertion also keeps malformed SDK header payloads non-fatal.
	malformed := identityHeaderReader{header: map[string]*commonpb.Payload{
		requestIDHeader:     {Data: []byte("not-json")},
		correlationIDHeader: {Data: make([]byte, maxPropagatedValueBytes+1)},
	}}
	if _, err := propagator.Extract(context.Background(), malformed); err != nil {
		t.Fatalf("Extract() returned an error for invalid optional identity: %v", err)
	}
}

func identityHeader(t *testing.T, requestID, correlationID string) *commonpb.Header {
	t.Helper()
	fields := make(map[string]*commonpb.Payload)
	for key, value := range map[string]string{requestIDHeader: requestID, correlationIDHeader: correlationID} {
		if value == "" {
			continue
		}
		payload, err := converter.GetDefaultDataConverter().ToPayload(value)
		if err != nil {
			t.Fatalf("encode identity header: %v", err)
		}
		fields[key] = payload
	}
	return &commonpb.Header{Fields: fields}
}

func observabilityWorkflow(ctx workflow.Context) error {
	workflow.GetLogger(ctx).Info("workflow started")
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Second})
	if err := workflow.SetUpdateHandler(ctx, "run-activity", func(updateCtx workflow.Context) error {
		updateCtx = workflow.WithActivityOptions(updateCtx, workflow.ActivityOptions{StartToCloseTimeout: time.Second})
		return workflow.ExecuteActivity(updateCtx, observabilityActivityName).Get(updateCtx, nil)
	}); err != nil {
		return err
	}
	if err := workflow.ExecuteActivity(ctx, observabilityActivityName).Get(ctx, nil); err != nil {
		return err
	}
	var childResult string
	if err := workflow.ExecuteChildWorkflow(ctx, "observability-child").Get(ctx, &childResult); err != nil {
		return err
	}
	var signal string
	workflow.GetSignalChannel(ctx, "continue").Receive(ctx, &signal)
	if err := workflow.ExecuteActivity(ctx, observabilityActivityName).Get(ctx, nil); err != nil {
		return err
	}
	return nil
}

func observabilityChildWorkflow(ctx workflow.Context) (string, error) {
	workflow.GetLogger(ctx).Info("child workflow started")
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Second})
	if err := workflow.ExecuteActivity(ctx, observabilityActivityName).Get(ctx, nil); err != nil {
		return "", err
	}
	return "done", nil
}

const observabilityActivityName = "observability-activity"

type identityHeaderWriter struct {
	header map[string]*commonpb.Payload
}

func (w identityHeaderWriter) Set(key string, payload *commonpb.Payload) { w.header[key] = payload }

type identityHeaderReader struct {
	header map[string]*commonpb.Payload
}

func (r identityHeaderReader) Get(key string) (*commonpb.Payload, bool) {
	payload, ok := r.header[key]
	return payload, ok
}

func (r identityHeaderReader) ForEachKey(fn func(string, *commonpb.Payload) error) error {
	for key, payload := range r.header {
		if err := fn(key, payload); err != nil {
			return err
		}
	}
	return nil
}
