package websocket

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"runtime/debug"
	"sync"
	"time"

	"go.uber.org/zap"

	commonjson "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/logger"
)

const (
	maxRegisteredHandlers = 1024
	maxOrderingKeyBytes   = 256
	maxHandlerStackBytes  = 32 << 10
)

// Handler processes a strictly decoded application message. The supplied
// context carries the connection correlation ID and authenticated principal.
type Handler[T any] func(context.Context, *Conn, *T) error

// HandlerOptions controls bounded concurrency for one message type.
//
// Parallelism defaults to one. Values greater than one require OrderingKey so
// messages for the same aggregate remain FIFO while independent aggregates may
// execute concurrently.
type HandlerOptions[T any] struct {
	Parallelism int
	OrderingKey func(*T) string
	Timeout     time.Duration
}

type registeredHandler struct {
	messageType string
	parallelism int
	timeout     time.Duration
	decode      func([]byte) (any, error)
	orderingKey func(any) (string, error)
	invoke      func(context.Context, *Conn, any) error
}

type handlerLane struct {
	mu   sync.Mutex
	tail chan struct{}
}

type connectionHandlerState struct {
	execution chan struct{}
	lanes     []handlerLane
}

// On registers a serial, per-connection handler. Registration is only allowed
// before Hub.Run so the dispatch table is immutable while serving traffic.
func On[T any](hub *Hub, messageType string, handler Handler[T]) error {
	return OnWithOptions(
		hub,
		messageType,
		HandlerOptions[T]{Parallelism: 1},
		handler,
	)
}

// OnWithOptions registers a strictly decoded, bounded handler.
func OnWithOptions[T any](
	hub *Hub,
	messageType string,
	options HandlerOptions[T],
	handler Handler[T],
) error {
	if hub == nil || handler == nil {
		return ErrInvalidOptions
	}
	if !validApplicationType(messageType) {
		return ErrInvalidMessage
	}
	if options.Parallelism == 0 {
		options.Parallelism = 1
	}
	if options.Parallelism < 1 ||
		options.Parallelism > hub.options.MaxInFlightHandlersPerConnection ||
		options.Timeout < 0 ||
		options.Timeout > maxHandlerTimeout ||
		(options.Parallelism > 1 && options.OrderingKey == nil) {
		return ErrInvalidOptions
	}

	timeout := options.Timeout
	if timeout == 0 || timeout > hub.options.HandlerTimeout {
		timeout = hub.options.HandlerTimeout
	}
	registration := &registeredHandler{
		messageType: messageType,
		parallelism: options.Parallelism,
		timeout:     timeout,
		decode: func(data []byte) (any, error) {
			trimmed := bytes.TrimSpace(data)
			if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
				return nil, ErrInvalidEnvelope
			}
			var message T
			if err := commonjson.UnmarshalStrict(trimmed, &message); err != nil {
				return nil, ErrInvalidEnvelope
			}
			return &message, nil
		},
		invoke: func(ctx context.Context, connection *Conn, decoded any) error {
			message, ok := decoded.(*T)
			if !ok {
				return ErrInvalidEnvelope
			}
			return handler(ctx, connection, message)
		},
	}
	if options.OrderingKey != nil {
		registration.orderingKey = func(decoded any) (key string, err error) {
			message, ok := decoded.(*T)
			if !ok {
				return "", ErrInvalidEnvelope
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					err = &handlerPanicError{recovered: recovered}
				}
			}()
			return options.OrderingKey(message), nil
		}
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.status != hubNew {
		return ErrClosed
	}
	if len(hub.handlerRegistry) >= maxRegisteredHandlers {
		return ErrOverloaded
	}
	if _, duplicate := hub.handlerRegistry[messageType]; duplicate {
		return ErrInvalidMessage
	}
	hub.handlerRegistry[messageType] = registration
	return nil
}

func (hub *Hub) dispatchApplication(
	connection *Conn,
	envelope Envelope,
) error {
	if hub == nil || connection == nil {
		return ErrInvalidMessage
	}
	hub.mu.RLock()
	registration := hub.handlerRegistry[envelope.Type]
	running := hub.status == hubRunning && connection.state == connectionOpen
	hub.mu.RUnlock()
	if !running {
		return ErrShuttingDown
	}
	if registration == nil {
		return ErrUnknownOperation
	}

	decoded, err := registration.decode(envelope.Data)
	if err != nil {
		return newProtocolError(ErrInvalidEnvelope, CodeInvalidMessage, false)
	}
	key := ""
	if registration.orderingKey != nil {
		key, err = registration.orderingKey(decoded)
		if err != nil {
			var panicError *handlerPanicError
			if errors.As(err, &panicError) {
				hub.metrics.handlerPanics.Add(1)
				hub.logHandlerPanic(
					connection.ctx,
					connection,
					registration,
					envelope.ID,
					panicError.recovered,
				)
				return newProtocolError(ErrInvalidMessage, CodeInternal, false)
			}
			return newProtocolError(ErrInvalidEnvelope, CodeInvalidMessage, false)
		}
		if !validRequiredTerm(key, maxOrderingKeyBytes) {
			return newProtocolError(ErrInvalidEnvelope, CodeInvalidMessage, false)
		}
	}

	if err := hub.acquireHandlerSlots(connection); err != nil {
		return err
	}
	state := connection.handlerState(registration)
	lane := state.lane(key)
	previous, completed := lane.reserve()

	hub.mu.Lock()
	if hub.status != hubRunning || connection.state != connectionOpen {
		hub.mu.Unlock()
		close(completed)
		hub.releaseHandlerSlots(connection)
		return ErrShuttingDown
	}
	hub.handlers.Add(1)
	hub.metrics.activeHandlers.Add(1)
	hub.mu.Unlock()

	invocation := handlerInvocation{
		connection:   connection,
		envelopeID:   envelope.ID,
		registration: registration,
		decoded:      decoded,
		state:        state,
		previous:     previous,
		completed:    completed,
	}
	if registration.parallelism == 1 {
		hub.executeHandler(invocation)
		return nil
	}
	go hub.executeHandler(invocation)
	return nil
}

type handlerInvocation struct {
	connection   *Conn
	envelopeID   string
	registration *registeredHandler
	decoded      any
	state        *connectionHandlerState
	previous     <-chan struct{}
	completed    chan struct{}
}

func (hub *Hub) executeHandler(invocation handlerInvocation) {
	defer func() {
		close(invocation.completed)
		hub.releaseHandlerSlots(invocation.connection)
		hub.metrics.activeHandlers.Add(-1)
		hub.handlers.Done()
	}()

	if invocation.previous != nil {
		select {
		case <-invocation.previous:
		case <-invocation.connection.ctx.Done():
			return
		}
	}
	select {
	case invocation.state.execution <- struct{}{}:
	case <-invocation.connection.ctx.Done():
		return
	}
	defer func() { <-invocation.state.execution }()

	ctx, cancel := context.WithTimeout(
		invocation.connection.ctx,
		invocation.registration.timeout,
	)
	defer cancel()
	ctx = logger.WithFields(
		ctx,
		logger.String("connection_id", invocation.connection.id),
		logger.String("message_id", invocation.envelopeID),
		logger.String("message_type", invocation.registration.messageType),
	)
	started := time.Now()
	log := logger.FromContext(ctx)
	log.Debug("websocket handler started")

	err := hub.invokeHandler(ctx, invocation)
	duration := time.Since(started)
	if err == nil {
		log.Debug("websocket handler completed", zap.Duration("duration", duration))
		return
	}
	hub.metrics.inboundErrors.Add(1)
	log.Warn(
		"websocket handler failed",
		zap.String("error_type", fmt.Sprintf("%T", err)),
		zap.Duration("duration", duration),
	)
	hub.enqueueProtocolError(invocation.connection, invocation.envelopeID, err)
}

func (hub *Hub) invokeHandler(
	ctx context.Context,
	invocation handlerInvocation,
) (err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		hub.metrics.handlerPanics.Add(1)
		hub.logHandlerPanic(
			ctx,
			invocation.connection,
			invocation.registration,
			invocation.envelopeID,
			recovered,
		)
		err = newProtocolError(ErrInvalidMessage, CodeInternal, false)
	}()
	return invocation.registration.invoke(
		ctx,
		invocation.connection,
		invocation.decoded,
	)
}

func (hub *Hub) acquireHandlerSlots(connection *Conn) error {
	select {
	case connection.handlerSlots <- struct{}{}:
	case <-connection.ctx.Done():
		return ErrShuttingDown
	}
	select {
	case hub.globalHandlerSlots <- struct{}{}:
		return nil
	case <-connection.ctx.Done():
		<-connection.handlerSlots
		return ErrShuttingDown
	}
}

func (hub *Hub) releaseHandlerSlots(connection *Conn) {
	<-hub.globalHandlerSlots
	<-connection.handlerSlots
}

func (connection *Conn) handlerState(
	registration *registeredHandler,
) *connectionHandlerState {
	connection.handlerMu.Lock()
	defer connection.handlerMu.Unlock()
	state := connection.handlerStates[registration.messageType]
	if state != nil {
		return state
	}
	state = &connectionHandlerState{
		execution: make(chan struct{}, registration.parallelism),
		lanes:     make([]handlerLane, registration.parallelism),
	}
	connection.handlerStates[registration.messageType] = state
	return state
}

func (state *connectionHandlerState) lane(key string) *handlerLane {
	if len(state.lanes) == 1 {
		return &state.lanes[0]
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	return &state.lanes[hasher.Sum64()%uint64(len(state.lanes))]
}

func (lane *handlerLane) reserve() (<-chan struct{}, chan struct{}) {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	previous := lane.tail
	completed := make(chan struct{})
	lane.tail = completed
	return previous, completed
}

type handlerPanicError struct {
	recovered any
}

func (*handlerPanicError) Error() string {
	return "websocket: handler panic"
}

func (hub *Hub) logHandlerPanic(
	ctx context.Context,
	connection *Conn,
	registration *registeredHandler,
	envelopeID string,
	recovered any,
) {
	ctx = logger.WithFields(
		ctx,
		logger.String("connection_id", connection.id),
		logger.String("message_id", envelopeID),
		logger.String("message_type", registration.messageType),
	)
	stack := debug.Stack()
	if len(stack) > maxHandlerStackBytes {
		stack = stack[:maxHandlerStackBytes]
	}
	logger.FromContext(ctx).Error(
		"websocket handler panic recovered",
		zap.String("panic_type", fmt.Sprintf("%T", recovered)),
		zap.ByteString("stack", stack),
	)
}
