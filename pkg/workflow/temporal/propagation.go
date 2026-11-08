package temporal

import (
	"context"
	"strconv"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"

	"github.com/huynhanx03/go-common/pkg/logger"
)

type contextPropagator struct{}

type propagatedIdentity struct {
	requestID     string
	correlationID string
}

type workflowIdentityKey struct{}

func (*contextPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	return injectIdentity(writer, propagatedIdentity{
		requestID:     logger.RequestID(ctx),
		correlationID: logger.CorrelationID(ctx),
	})
}

func (*contextPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	identity := extractIdentity(reader)
	if identity.requestID != "" {
		ctx = logger.WithRequestID(ctx, identity.requestID)
	}
	if identity.correlationID != "" {
		ctx = logger.WithCorrelationID(ctx, identity.correlationID)
	}
	return ctx, nil
}

func (*contextPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	identity, _ := ctx.Value(workflowIdentityKey{}).(propagatedIdentity)
	return injectIdentity(writer, identity)
}

func (*contextPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	identity, _ := ctx.Value(workflowIdentityKey{}).(propagatedIdentity)
	incoming := extractIdentity(reader)
	if incoming.requestID != "" {
		identity.requestID = incoming.requestID
		identity.correlationID = incoming.requestID
	}
	if incoming.correlationID != "" {
		identity.correlationID = incoming.correlationID
	}
	return workflow.WithValue(ctx, workflowIdentityKey{}, identity), nil
}

func injectIdentity(writer workflow.HeaderWriter, identity propagatedIdentity) error {
	converter := converter.GetDefaultDataConverter()
	for _, header := range []struct {
		key   string
		value string
	}{
		{key: requestIDHeader, value: identity.requestID},
		{key: correlationIDHeader, value: identity.correlationID},
	} {
		if logger.ValidateRequestID(header.value) != nil {
			continue
		}
		payload, err := converter.ToPayload(header.value)
		if err != nil || payloadSize(payload) > maxPropagatedValueBytes {
			continue
		}
		writer.Set(header.key, payload)
	}
	return nil
}

func extractIdentity(reader workflow.HeaderReader) propagatedIdentity {
	identity := propagatedIdentity{
		requestID:     readIdentity(reader, requestIDHeader),
		correlationID: readIdentity(reader, correlationIDHeader),
	}
	if identity.requestID != "" && identity.correlationID == "" {
		identity.correlationID = identity.requestID
	}
	return identity
}

func readIdentity(reader workflow.HeaderReader, key string) string {
	payload, ok := reader.Get(key)
	if !ok || payload == nil || payloadSize(payload) > maxPropagatedValueBytes {
		return ""
	}
	var value string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &value); err != nil || logger.ValidateRequestID(value) != nil {
		return ""
	}
	return value
}

func payloadSize(payload *commonpb.Payload) int {
	size := len(payload.Data)
	for key, value := range payload.Metadata {
		size += len(key) + len(value)
	}
	return size
}

func identityFields(identity propagatedIdentity) []any {
	fields := make([]any, 0, 4)
	if identity.requestID != "" {
		fields = append(fields, requestIDHeader, identity.requestID)
	}
	if identity.correlationID != "" {
		fields = append(fields, correlationIDHeader, identity.correlationID)
	}
	return fields
}

func workflowLogFields(ctx workflow.Context) []any {
	identity, _ := ctx.Value(workflowIdentityKey{}).(propagatedIdentity)
	fields := identityFields(identity)
	info := workflow.GetInfo(ctx)
	fields = append(fields,
		workflowIDField, info.WorkflowExecution.ID,
		runIDField, info.WorkflowExecution.RunID,
		"attempt", strconv.Itoa(int(info.Attempt)),
	)
	return fields
}

var _ workflow.ContextPropagator = (*contextPropagator)(nil)
