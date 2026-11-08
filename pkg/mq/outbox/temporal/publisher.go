// Package temporaloutbox starts Temporal Workflows for committed outbox messages.
package temporaloutbox

import (
	"context"
	"errors"
	"maps"
	"strings"
	"unicode"
	"unicode/utf8"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

// WorkflowStarter is the subset of the Temporal SDK client used by Publisher.
type WorkflowStarter interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, any, ...any) (client.WorkflowRun, error)
}

// Route selects the Workflow and stable workflow ID for one outbox destination.
type Route struct {
	Workflow   string
	TaskQueue  string
	Decode     func(context.Context, outbox.Message) (any, error)
	WorkflowID func(outbox.Message) (string, error)
	Options    client.StartWorkflowOptions
}

// Publisher translates committed outbox messages into idempotent Workflow starts.
// It borrows the supplied Temporal client and keeps an immutable route snapshot.
type Publisher struct {
	starter WorkflowStarter
	routes  map[string]Route
}

// NewPublisher validates and copies the destination allowlist and native start options.
func NewPublisher(starter WorkflowStarter, routes map[string]Route) (*Publisher, error) {
	if starter == nil || len(routes) == 0 || len(routes) > maximumRoutes {
		return nil, ErrInvalidRoutes
	}

	ownedRoutes := make(map[string]Route, len(routes))
	for destination, route := range routes {
		if !validRouteText(destination) || !validRouteText(route.Workflow) ||
			!validRouteText(route.TaskQueue) || route.Decode == nil || route.WorkflowID == nil {
			return nil, ErrInvalidRoutes
		}
		route.Options = cloneStartWorkflowOptions(route.Options)
		ownedRoutes[strings.Clone(destination)] = route
	}
	return &Publisher{starter: starter, routes: ownedRoutes}, nil
}

// Publish decodes the allowlisted payload and starts it using its stable Workflow ID.
func (publisher *Publisher) Publish(ctx context.Context, message outbox.Message) (outbox.PublishAck, error) {
	if err := ctx.Err(); err != nil {
		return outbox.PublishAck{}, err
	}
	route, exists := publisher.routes[message.Destination]
	if !exists {
		return outbox.PublishAck{}, permanent(CodeRouteNotConfigured, ErrRouteNotConfigured)
	}
	if !validRouteText(message.ID) || (message.Metadata.CorrelationID != "" && logger.ValidateRequestID(message.Metadata.CorrelationID) != nil) {
		return outbox.PublishAck{}, permanent(CodeInvalidMessage, ErrInvalidMessage)
	}
	input, err := route.Decode(ctx, message)
	if err != nil {
		return outbox.PublishAck{}, permanent(CodeInvalidMessage, errors.Join(ErrInvalidMessage, err))
	}
	workflowID, err := route.WorkflowID(message)
	if err != nil || !validWorkflowID(workflowID) {
		return outbox.PublishAck{}, permanent(CodeInvalidMessage, errors.Join(ErrInvalidMessage, err))
	}
	if message.Metadata.CorrelationID != "" {
		ctx = logger.WithCorrelationID(ctx, message.Metadata.CorrelationID)
	}

	options := cloneStartWorkflowOptions(route.Options)
	options.ID = workflowID
	options.TaskQueue = route.TaskQueue
	options.WorkflowIDConflictPolicy = enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL
	options.WorkflowIDReusePolicy = enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	options.WorkflowExecutionErrorWhenAlreadyStarted = true
	if _, err := publisher.starter.ExecuteWorkflow(ctx, options, route.Workflow, input); err != nil {
		var duplicate *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &duplicate) {
			return outbox.PublishAck{Reference: workflowID}, nil
		}
		return outbox.PublishAck{}, classifyStartFailure(err)
	}
	return outbox.PublishAck{Reference: workflowID}, nil
}

func cloneStartWorkflowOptions(options client.StartWorkflowOptions) client.StartWorkflowOptions {
	options.Memo = maps.Clone(options.Memo)
	options.SearchAttributes = maps.Clone(options.SearchAttributes)
	return options
}

func classifyStartFailure(err error) error {
	var invalidArgument *serviceerror.InvalidArgument
	var namespaceMissing *serviceerror.NamespaceNotFound
	var permissionDenied *serviceerror.PermissionDenied
	if errors.As(err, &invalidArgument) || errors.As(err, &namespaceMissing) || errors.As(err, &permissionDenied) {
		return &outbox.PublishError{Code: CodeWorkflowStartDenied, Retryable: false, Cause: err}
	}
	return &outbox.PublishError{Code: CodeWorkflowStart, Retryable: true, Ambiguous: true, Cause: err}
}

func permanent(code string, cause error) error {
	return &outbox.PublishError{Code: code, Retryable: false, Cause: cause}
}

func validWorkflowID(value string) bool {
	return validText(value, maximumWorkflowIDBytes)
}

func validRouteText(value string) bool {
	return validText(value, maximumRouteTextBytes)
}

func validText(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
