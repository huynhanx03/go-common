package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"

	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func (hub *Hub) subscribe(connection *Conn, topic string) error {
	if !validRequiredTerm(topic, maxTopicBytes) {
		return ErrInvalidEnvelope
	}
	hub.mu.RLock()
	accepting := hub.status == hubRunning && connection.state == connectionOpen
	hub.mu.RUnlock()
	if !accepting {
		return ErrShuttingDown
	}
	if err := authorizeTopic(
		connection.authorize,
		connection.ctx,
		connection.principal,
		topic,
	); err != nil {
		if connection.ctx.Err() != nil {
			return ErrShuttingDown
		}
		return ErrForbiddenTopic
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.status != hubRunning || connection.state != connectionOpen {
		return ErrShuttingDown
	}
	if _, exists := connection.topics[topic]; exists {
		return nil
	}
	if len(connection.topics) >= hub.options.MaxSubscriptions {
		return ErrOverloaded
	}
	connection.topics[topic] = struct{}{}
	addIndexedConnection(hub.state.topics, topic, connection)
	return nil
}

func authorizeTopic(
	authorize TopicAuthorizer,
	ctx context.Context,
	principal authentication.Principal,
	topic string,
) (err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		stack := debug.Stack()
		if len(stack) > maxHandlerStackBytes {
			stack = stack[:maxHandlerStackBytes]
		}
		logContext := logger.WithFields(ctx, logger.String("topic", topic))
		logger.FromContext(logContext).Error(
			"websocket topic authorizer panic recovered",
			zap.String("panic_type", fmt.Sprintf("%T", recovered)),
			zap.ByteString("stack", stack),
		)
		err = ErrForbiddenTopic
	}()
	return authorize(ctx, principal, topic)
}

func (hub *Hub) unsubscribe(connection *Conn, topic string) error {
	if !validRequiredTerm(topic, maxTopicBytes) {
		return ErrInvalidEnvelope
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if connection.state != connectionOpen {
		return ErrClosed
	}
	delete(connection.topics, topic)
	removeIndexedConnection(hub.state.topics, topic, connection.id)
	return nil
}

func (hub *Hub) handleControl(connection *Conn, envelope Envelope) error {
	var err error
	switch envelope.Operation {
	case OperationSubscribe:
		err = hub.subscribe(connection, envelope.Topic)
	case OperationUnsubscribe:
		err = hub.unsubscribe(connection, envelope.Topic)
	case OperationPing:
		return hub.enqueueEnvelope(connection, Envelope{
			Version:   ProtocolVersion,
			Operation: OperationPong,
			ID:        envelope.ID,
			Type:      ProtocolTypePong,
		})
	case OperationPong:
		return nil
	default:
		err = ErrUnknownOperation
	}
	if err != nil {
		hub.enqueueProtocolError(connection, envelope.ID, err)
		return err
	}
	operation := OperationSubscribed
	if envelope.Operation == OperationUnsubscribe {
		operation = OperationUnsubscribed
	}
	return hub.enqueueEnvelope(connection, Envelope{
		Version:   ProtocolVersion,
		Operation: operation,
		ID:        envelope.ID,
		Topic:     envelope.Topic,
		Type:      ProtocolTypeSubscription,
	})
}

func (hub *Hub) enqueueProtocolError(
	connection *Conn,
	requestID string,
	err error,
) {
	code := CodeInternal
	retryable := false
	switch {
	case errors.Is(err, ErrInvalidEnvelope):
		code = CodeInvalidMessage
	case errors.Is(err, ErrUnsupportedVersion):
		code = CodeUnsupportedVersion
	case errors.Is(err, ErrUnknownOperation):
		code = CodeUnknownOperation
	case errors.Is(err, ErrForbiddenTopic):
		code = CodeForbiddenTopic
	case errors.Is(err, ErrOverloaded):
		code = CodeOverloaded
		retryable = true
	case errors.Is(err, ErrShuttingDown), errors.Is(err, ErrClosed):
		code = CodeShuttingDown
		retryable = true
	}
	data, _ := json.Marshal(struct {
		Code      ErrorCode `json:"code"`
		Retryable bool      `json:"retryable"`
	}{
		Code:      code,
		Retryable: retryable,
	})
	_ = hub.enqueueEnvelope(connection, Envelope{
		Version:   ProtocolVersion,
		Operation: OperationError,
		ID:        requestID,
		Type:      ProtocolTypeError,
		Data:      data,
	})
}

func (hub *Hub) enqueueEnvelope(connection *Conn, envelope Envelope) error {
	encoded, err := EncodeEnvelope(envelope, hub.options.MaxOutboundMessageBytes)
	if err != nil {
		return err
	}
	return hub.offerFrame(
		connection,
		&encodedFrame{payload: encoded},
		DeliveryCritical,
		"",
	)
}
