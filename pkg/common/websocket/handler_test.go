package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/correlation"
	"github.com/huynhanx03/go-common/pkg/logger"
)

type handlerPayload struct {
	Key   string `json:"key"`
	Value int    `json:"value"`
}

func handlerEnvelope(key string, value int) Envelope {
	data, _ := json.Marshal(handlerPayload{Key: key, Value: value})
	return Envelope{
		Version:   ProtocolVersion,
		Operation: OperationEvent,
		ID:        "handler-message",
		Type:      "handler.test.v1",
		Data:      data,
	}
}

func TestHandlerContextCarriesTransportCorrelationFields(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.DebugLevel)
	hub, connection := handlerUnitHub(t, func(hub *Hub) error {
		return On(hub, "handler.test.v1", func(
			ctx context.Context,
			_ *Conn,
			_ *handlerPayload,
		) error {
			logger.FromContext(ctx).Info("application handler")
			return nil
		})
	})
	connection.ctx = logger.WithContext(connection.ctx, zap.New(core))
	connection.ctx = correlation.WithContext(connection.ctx, "connection-cid")

	envelope := handlerEnvelope("aggregate-1", 1)
	envelope.ID = "message-1"
	if err := hub.dispatchApplication(connection, envelope); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	entries := logs.FilterMessage("application handler").All()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	for key, want := range map[string]any{
		"correlation_id": "connection-cid",
		"connection_id":  "handler-connection",
		"message_id":     "message-1",
		"message_type":   "handler.test.v1",
	} {
		if fields[key] != want {
			t.Fatalf("%s = %v, want %v", key, fields[key], want)
		}
	}
}

func handlerUnitHub(
	t *testing.T,
	register func(*Hub) error,
) (*Hub, *Conn) {
	t.Helper()

	options := mailboxOptions()
	options.MaxInFlightHandlersPerConnection = 4
	options.MaxInFlightHandlers = 8
	hub, err := NewHub(options)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	if err := register(hub); err != nil {
		t.Fatalf("register: %v", err)
	}
	hub.status = hubRunning
	hub.runCtx = context.Background()
	connection := newUnitConnection(t, hub, "handler-connection", "user:handler")
	return hub, connection
}

func TestHandlerDefaultIsSerialAndOrderedPerConnection(t *testing.T) {
	t.Parallel()

	started := make(chan int, 2)
	releaseFirst := make(chan struct{})
	hub, connection := handlerUnitHub(t, func(hub *Hub) error {
		return On(hub, "handler.test.v1", func(
			_ context.Context,
			_ *Conn,
			message *handlerPayload,
		) error {
			started <- message.Value
			if message.Value == 1 {
				<-releaseFirst
			}
			return nil
		})
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- hub.dispatchApplication(connection, handlerEnvelope("same", 1))
	}()
	if value := <-started; value != 1 {
		t.Fatalf("first value = %d", value)
	}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- hub.dispatchApplication(connection, handlerEnvelope("same", 2))
	}()
	select {
	case value := <-started:
		t.Fatalf("second handler started before first completed: %d", value)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if value := <-started; value != 2 {
		t.Fatalf("second value = %d", value)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
}

func TestHandlerParallelismPreservesSameKeySerialization(t *testing.T) {
	t.Parallel()

	started := make(chan string, 3)
	releases := map[string]chan struct{}{
		"a": make(chan struct{}),
		"b": make(chan struct{}),
	}
	hub, connection := handlerUnitHub(t, func(hub *Hub) error {
		return OnWithOptions(
			hub,
			"handler.test.v1",
			HandlerOptions[handlerPayload]{
				Parallelism: 2,
				OrderingKey: func(message *handlerPayload) string {
					return message.Key
				},
				Timeout: time.Second,
			},
			func(_ context.Context, _ *Conn, message *handlerPayload) error {
				started <- message.Key
				<-releases[message.Key]
				return nil
			},
		)
	})

	if err := hub.dispatchApplication(connection, handlerEnvelope("a", 1)); err != nil {
		t.Fatalf("dispatch a1: %v", err)
	}
	if err := hub.dispatchApplication(connection, handlerEnvelope("a", 2)); err != nil {
		t.Fatalf("dispatch a2: %v", err)
	}
	if err := hub.dispatchApplication(connection, handlerEnvelope("b", 3)); err != nil {
		t.Fatalf("dispatch b: %v", err)
	}

	observed := map[string]int{}
	for len(observed) < 2 {
		select {
		case key := <-started:
			observed[key]++
		case <-time.After(time.Second):
			t.Fatal("different keys did not run concurrently")
		}
	}
	if observed["a"] != 1 || observed["b"] != 1 {
		t.Fatalf("started = %+v; same key overlapped", observed)
	}
	close(releases["a"])
	select {
	case key := <-started:
		if key != "a" {
			t.Fatalf("next key = %q", key)
		}
	case <-time.After(time.Second):
		t.Fatal("second same-key handler did not start")
	}
	close(releases["b"])
	waitForCondition(t, func() bool {
		return hub.Metrics().ActiveHandlers == 0
	})
}

func TestHandlerPanicIsContainedAndSlotsAreReleased(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	hub, connection := handlerUnitHub(t, func(hub *Hub) error {
		return On(hub, "handler.test.v1", func(
			context.Context,
			*Conn,
			*handlerPayload,
		) error {
			if calls.Add(1) == 1 {
				panic("token=secret")
			}
			return nil
		})
	})
	if err := hub.dispatchApplication(connection, handlerEnvelope("a", 1)); err != nil {
		t.Fatalf("panic dispatch: %v", err)
	}
	if err := hub.dispatchApplication(connection, handlerEnvelope("a", 2)); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if calls.Load() != 2 ||
		hub.Metrics().HandlerPanics != 1 ||
		hub.Metrics().ActiveHandlers != 0 {
		t.Fatalf("calls=%d metrics=%+v", calls.Load(), hub.Metrics())
	}
}

func TestHandlerRegistrationAndDecodeFailClosed(t *testing.T) {
	t.Parallel()

	hub, err := NewHub(validHubOptions())
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	handler := func(context.Context, *Conn, *handlerPayload) error { return nil }
	if err := On[handlerPayload](nil, "handler.test.v1", handler); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nil Hub registration error = %v", err)
	}
	if err := On(hub, "", handler); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("empty type error = %v", err)
	}
	if err := OnWithOptions(
		hub,
		"handler.test.v1",
		HandlerOptions[handlerPayload]{Parallelism: 2},
		handler,
	); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("parallel registration error = %v", err)
	}
	if err := On(hub, "handler.test.v1", handler); err != nil {
		t.Fatalf("On: %v", err)
	}
	if err := On(hub, "handler.test.v1", handler); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("duplicate registration error = %v", err)
	}

	hub.status = hubRunning
	hub.runCtx = context.Background()
	connection := newUnitConnection(t, hub, "handler-connection", "user:handler")
	envelope := handlerEnvelope("a", 1)
	envelope.Data = json.RawMessage(`{"unknown":true}`)
	if err := hub.dispatchApplication(connection, envelope); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("strict decode error = %v", err)
	}
	if err := On(hub, "late.v1", handler); !errors.Is(err, ErrClosed) {
		t.Fatalf("late registration error = %v", err)
	}
}

func TestShutdownReportsNonCompliantHandler(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	options := mailboxOptions()
	options.ShutdownTimeout = 50 * time.Millisecond
	hub, err := NewHub(options)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	if err := On(hub, "handler.test.v1", func(
		context.Context,
		*Conn,
		*handlerPayload,
	) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("On: %v", err)
	}
	hub.status = hubRunning
	hub.runCtx, hub.runCancel = context.WithCancel(context.Background())
	connection := newUnitConnection(t, hub, "handler-connection", "user:handler")
	go func() {
		_ = hub.dispatchApplication(connection, handlerEnvelope("a", 1))
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = hub.Shutdown(ctx)
	var undrained *UndrainedError
	if !errors.As(err, &undrained) || undrained.Handlers != 1 {
		t.Fatalf("Shutdown error = %v", err)
	}
	close(release)
	waitForCondition(t, func() bool {
		return hub.Metrics().ActiveHandlers == 0
	})
}

func TestHandlerGlobalAndConnectionSlotsAreBounded(t *testing.T) {
	t.Parallel()

	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	hub, connection := handlerUnitHub(t, func(hub *Hub) error {
		return OnWithOptions(
			hub,
			"handler.test.v1",
			HandlerOptions[handlerPayload]{
				Parallelism: 4,
				OrderingKey: func(message *handlerPayload) string { return message.Key },
			},
			func(context.Context, *Conn, *handlerPayload) error {
				current := active.Add(1)
				for {
					observed := maximum.Load()
					if current <= observed || maximum.CompareAndSwap(observed, current) {
						break
					}
				}
				once.Do(func() {
					go func() {
						time.Sleep(20 * time.Millisecond)
						close(release)
					}()
				})
				<-release
				active.Add(-1)
				return nil
			},
		)
	})
	for index := 0; index < 8; index++ {
		envelope := handlerEnvelope(string(rune('a'+index)), index)
		go func() {
			_ = hub.dispatchApplication(connection, envelope)
		}()
	}
	waitForCondition(t, func() bool {
		return maximum.Load() > 0
	})
	waitForCondition(t, func() bool {
		return hub.Metrics().ActiveHandlers == 0
	})
	if maximum.Load() > int32(hub.options.MaxInFlightHandlersPerConnection) {
		t.Fatalf("max active = %d", maximum.Load())
	}
}
