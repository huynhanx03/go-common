package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type transportRead struct {
	payload []byte
	text    bool
	err     error
}

type orderedTransport struct {
	reads       chan transportRead
	writeNotify chan struct{}
	closeNotify chan CloseOptions
	writeError  error
	pingError   error

	mu              sync.Mutex
	writes          [][]byte
	activeWriters   int
	maximumWriters  int
	closeOnce       sync.Once
	closeNowInvoked atomic.Bool
}

func newOrderedTransport() *orderedTransport {
	return &orderedTransport{
		reads:       make(chan transportRead, 8),
		writeNotify: make(chan struct{}, 32),
		closeNotify: make(chan CloseOptions, 1),
	}
}

func (*orderedTransport) SetReadLimit(int64) {}

func (transport *orderedTransport) Read(
	ctx context.Context,
) ([]byte, bool, error) {
	select {
	case item := <-transport.reads:
		return item.payload, item.text, item.err
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

func (transport *orderedTransport) Write(
	ctx context.Context,
	payload []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transport.writeError != nil {
		return transport.writeError
	}
	transport.mu.Lock()
	transport.activeWriters++
	if transport.activeWriters > transport.maximumWriters {
		transport.maximumWriters = transport.activeWriters
	}
	transport.writes = append(transport.writes, append([]byte(nil), payload...))
	transport.activeWriters--
	transport.mu.Unlock()
	select {
	case transport.writeNotify <- struct{}{}:
	default:
	}
	return nil
}

func (transport *orderedTransport) Ping(ctx context.Context) error {
	if transport.pingError != nil {
		return transport.pingError
	}
	return ctx.Err()
}

func (transport *orderedTransport) Close(code int, reason string) error {
	transport.closeOnce.Do(func() {
		transport.closeNotify <- CloseOptions{Code: code, Reason: reason}
	})
	return nil
}

func (transport *orderedTransport) CloseNow() error {
	transport.closeNowInvoked.Store(true)
	return nil
}

func attachOrderedConnection(
	t *testing.T,
	hub *Hub,
	transport *orderedTransport,
) *Conn {
	t.Helper()

	if err := hub.attachConnection(
		context.Background(),
		authenticatedPrincipal("user:ordered"),
		"192.0.2.44",
		func(context.Context, authentication.Principal, string) error { return nil },
		transport,
	); err != nil {
		t.Fatalf("attachConnection: %v", err)
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for _, connection := range hub.state.connections {
		return connection
	}
	t.Fatal("attached connection was not indexed")
	return nil
}

func attachPumpConnection(
	t *testing.T,
	options Options,
	principal authentication.Principal,
	transport *orderedTransport,
) (*Hub, *Conn) {
	t.Helper()

	hub := newUnitHub(t, options)
	runContext, cancelRun := context.WithCancel(context.Background())
	hub.runCtx = runContext
	hub.runCancel = cancelRun
	if err := hub.attachConnection(
		context.Background(),
		principal,
		"192.0.2.44",
		func(context.Context, authentication.Principal, string) error { return nil },
		transport,
	); err != nil {
		cancelRun()
		t.Fatalf("attachConnection: %v", err)
	}
	hub.mu.RLock()
	var connection *Conn
	for _, current := range hub.state.connections {
		if current.principal.Subject == principal.Subject {
			connection = current
			break
		}
	}
	hub.mu.RUnlock()
	if connection == nil {
		t.Fatal("attached connection was not indexed")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := hub.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return hub, connection
}

func TestWriterPumpPreservesOrderAndUsesOneWriter(t *testing.T) {
	t.Parallel()

	options := validHubOptions()
	options.WriteQueueCapacity = 8
	options.WriteQueueByteCapacity = 4096
	options.MaxHubQueuedBytes = 8192
	options.MaxOutboundMessageBytes = 1024
	hub := newUnitHub(t, options)
	hub.runCtx, hub.runCancel = context.WithCancel(context.Background())
	transport := newOrderedTransport()
	connection := attachOrderedConnection(t, hub, transport)

	for index := 1; index <= 3; index++ {
		message := outboundMessage(
			DeliveryCritical,
			"",
			string(rune('0'+index)),
		)
		message.Envelope.ID = string(rune('0' + index))
		if err := connection.Send(context.Background(), message); err != nil {
			t.Fatalf("Send[%d]: %v", index, err)
		}
	}
	for index := 0; index < 3; index++ {
		select {
		case <-transport.writeNotify:
		case <-time.After(time.Second):
			t.Fatal("writer did not flush queued frame")
		}
	}

	transport.mu.Lock()
	writes := append([][]byte(nil), transport.writes...)
	maximumWriters := transport.maximumWriters
	transport.mu.Unlock()
	if len(writes) != 3 || maximumWriters != 1 {
		t.Fatalf("writes=%d maximum concurrent writers=%d", len(writes), maximumWriters)
	}
	for index, frame := range writes {
		var envelope Envelope
		if err := json.Unmarshal(frame, &envelope); err != nil {
			t.Fatalf("decode write[%d]: %v", index, err)
		}
		want := string(rune('1' + index))
		if envelope.ID != want {
			t.Fatalf("write[%d] ID=%q want=%q", index, envelope.ID, want)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestReadPumpDispatchesEventsAnswersPingAndRejectsBinaryFrames(t *testing.T) {
	t.Parallel()

	options := validHubOptions()
	hub, err := NewHub(testLogger(), options)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	handled := make(chan handlerPayload, 1)
	if err := On(hub, "handler.test.v1", func(
		_ context.Context,
		_ *Conn,
		message *handlerPayload,
	) error {
		handled <- *message
		return nil
	}); err != nil {
		t.Fatalf("On: %v", err)
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	hub.status = hubRunning
	hub.runCtx = runContext
	hub.runCancel = cancelRun
	transport := newOrderedTransport()
	if err := hub.attachConnection(
		context.Background(),
		authenticatedPrincipal("user:read-pump"),
		"192.0.2.44",
		func(context.Context, authentication.Principal, string) error { return nil },
		transport,
	); err != nil {
		cancelRun()
		t.Fatalf("attachConnection: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := hub.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	ping, err := EncodeEnvelope(Envelope{
		Version: ProtocolVersion, Operation: OperationPing,
		ID: "ping-1", Type: ProtocolTypePing,
	}, options.MaxOutboundMessageBytes)
	if err != nil {
		t.Fatalf("EncodeEnvelope ping: %v", err)
	}
	transport.reads <- transportRead{payload: ping, text: true}
	select {
	case <-transport.writeNotify:
	case <-time.After(time.Second):
		t.Fatal("ping did not produce a response")
	}
	transport.mu.Lock()
	pongFrame := append([]byte(nil), transport.writes[0]...)
	transport.mu.Unlock()
	var pong Envelope
	if err := json.Unmarshal(pongFrame, &pong); err != nil {
		t.Fatalf("decode pong: %v", err)
	}
	if pong.Operation != OperationPong || pong.ID != "ping-1" {
		t.Fatalf("pong = %+v; want matching pong response", pong)
	}

	transport.reads <- transportRead{payload: []byte("{"), text: true}
	select {
	case <-transport.writeNotify:
	case <-time.After(time.Second):
		t.Fatal("malformed frame did not produce a protocol error")
	}
	transport.mu.Lock()
	protocolErrorFrame := append([]byte(nil), transport.writes[1]...)
	transport.mu.Unlock()
	var protocolError Envelope
	if err := json.Unmarshal(protocolErrorFrame, &protocolError); err != nil {
		t.Fatalf("decode protocol error: %v", err)
	}
	var protocolErrorData struct {
		Code      ErrorCode `json:"code"`
		Retryable bool      `json:"retryable"`
	}
	if protocolError.Operation != OperationError ||
		json.Unmarshal(protocolError.Data, &protocolErrorData) != nil ||
		protocolErrorData.Code != CodeInvalidMessage || protocolErrorData.Retryable {
		t.Fatalf("malformed frame response = %+v data=%s", protocolError, protocolError.Data)
	}

	data, err := json.Marshal(handlerPayload{Key: "account-1", Value: 42})
	if err != nil {
		t.Fatalf("marshal handler payload: %v", err)
	}
	event, err := EncodeEnvelope(Envelope{
		Version: ProtocolVersion, Operation: OperationEvent,
		ID: "event-1", Type: "handler.test.v1", Data: data,
	}, options.MaxOutboundMessageBytes)
	if err != nil {
		t.Fatalf("EncodeEnvelope event: %v", err)
	}
	transport.reads <- transportRead{payload: event, text: true}
	select {
	case got := <-handled:
		if got != (handlerPayload{Key: "account-1", Value: 42}) {
			t.Fatalf("handled event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("application event was not dispatched")
	}

	transport.reads <- transportRead{payload: []byte("binary"), text: false}
	select {
	case closeOptions := <-transport.closeNotify:
		if closeOptions.Code != CloseUnsupportedData {
			t.Fatalf("binary frame close = %+v; want unsupported data", closeOptions)
		}
	case <-time.After(time.Second):
		t.Fatal("binary frame did not close the connection")
	}
	if got := hub.Metrics(); got.InboundAccepted != 2 || got.InboundInvalid != 2 {
		t.Fatalf("inbound metrics = %+v; want two accepted and two invalid", got)
	}
}

func TestWriterPumpClosesExpiredAuthenticatedConnection(t *testing.T) {
	t.Parallel()

	principal := authenticatedPrincipal("user:expired-pump")
	principal.AuthenticatedUntil = time.Now().Add(-time.Second)
	transport := newOrderedTransport()
	hub, _ := attachPumpConnection(t, validHubOptions(), principal, transport)
	select {
	case closeOptions := <-transport.closeNotify:
		if closeOptions.Code != CloseAuthenticationGone {
			t.Fatalf("expired principal close = %+v; want authentication-expired", closeOptions)
		}
	case <-time.After(time.Second):
		t.Fatal("expired principal connection remained open")
	}
	if got := hub.Metrics().Closes.Auth; got != 1 {
		t.Fatalf("authentication close metric = %d; want 1", got)
	}
}

func TestWriterPumpReportsPingFailureAndClosesConnection(t *testing.T) {
	t.Parallel()

	options := validHubOptions()
	options.PingInterval = 20 * time.Millisecond
	options.PongTimeout = 5 * time.Millisecond
	transport := newOrderedTransport()
	transport.pingError = errors.New("ping failed")
	hub, _ := attachPumpConnection(
		t,
		options,
		authenticatedPrincipal("user:ping-failure"),
		transport,
	)
	select {
	case closeOptions := <-transport.closeNotify:
		if closeOptions.Code != CloseInternalError {
			t.Fatalf("ping failure close = %+v; want internal error", closeOptions)
		}
	case <-time.After(time.Second):
		t.Fatal("ping failure did not close the connection")
	}
	if got := hub.Metrics().OutboundErrors; got != 1 {
		t.Fatalf("outbound errors = %d; want one failed ping", got)
	}
}

func TestWriterPumpClosesConnectionAfterWriteFailure(t *testing.T) {
	t.Parallel()

	transport := newOrderedTransport()
	transport.writeError = errors.New("write failed")
	hub, connection := attachPumpConnection(
		t,
		validHubOptions(),
		authenticatedPrincipal("user:write-failure"),
		transport,
	)
	if err := connection.Send(
		context.Background(),
		outboundMessage(DeliveryCritical, "", "important"),
	); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case closeOptions := <-transport.closeNotify:
		if closeOptions.Code != CloseInternalError {
			t.Fatalf("write failure close = %+v; want internal error", closeOptions)
		}
	case <-time.After(time.Second):
		t.Fatal("write failure did not close the connection")
	}
	if got := hub.Metrics().OutboundErrors; got != 1 {
		t.Fatalf("outbound errors = %d; want one failed write", got)
	}
}

func TestConnectionPublicMethodsRemainHubOwned(t *testing.T) {
	t.Parallel()

	var nilConnection *Conn
	if nilConnection.ID() != "" ||
		nilConnection.Principal().Authenticated ||
		!errors.Is(nilConnection.Send(context.Background(), Message{}), ErrClosed) ||
		!errors.Is(nilConnection.Close(context.Background(), DefaultCloseOptions()), ErrClosed) {
		t.Fatal("nil connection methods did not fail closed")
	}

	hub := newUnitHub(t, mailboxOptions())
	connection := newUnitConnection(t, hub, "connection-public", "user:public")
	if connection.ID() != "connection-public" ||
		connection.Principal().Subject != "user:public" {
		t.Fatalf("connection identity = %q %+v", connection.ID(), connection.Principal())
	}
	if err := connection.Send(
		context.Background(),
		outboundMessage(DeliveryEphemeral, "", "from-connection"),
	); err != nil {
		t.Fatalf("Conn.Send: %v", err)
	}
	if connection.mailbox.items != 1 {
		t.Fatalf("mailbox items = %d", connection.mailbox.items)
	}
	if err := connection.Close(
		context.Background(),
		CloseOptions{Code: CloseNormal, Reason: "client requested"},
	); err != nil {
		t.Fatalf("Conn.Close: %v", err)
	}
	if connection.state != connectionClosing {
		t.Fatalf("connection state = %d", connection.state)
	}
}

func TestMailboxGlobalBudgetAndOversizeAdmission(t *testing.T) {
	t.Parallel()

	options := mailboxOptions()
	options.WriteQueueCapacity = 2
	options.WriteQueueByteCapacity = 512
	options.MaxHubQueuedBytes = 512
	hub := newUnitHub(t, options)
	for index := 0; index < 16; index++ {
		connection := newUnitConnection(
			t,
			hub,
			"global-"+string(rune('a'+index)),
			"user:"+string(rune('a'+index)),
		)
		if err := hub.SendToConnection(
			context.Background(),
			connection.id,
			outboundMessage(DeliveryEphemeral, "", "global-budget"),
		); err != nil {
			t.Fatalf("global Send[%d]: %v", index, err)
		}
	}
	snapshot := hub.Metrics()
	if snapshot.QueuedBytes > options.MaxHubQueuedBytes ||
		snapshot.DroppedEphemeral == 0 {
		t.Fatalf("global budget metrics = %+v", snapshot)
	}

	before := hub.Metrics()
	oversize := outboundMessage(DeliveryCritical, "", "oversize")
	oversize.Envelope.Data = json.RawMessage(
		`{"value":"` + strings.Repeat("x", 512) + `"}`,
	)
	if err := hub.Broadcast(
		context.Background(),
		"unused",
		oversize,
	); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	after := hub.Metrics()
	if after.QueuedItems != before.QueuedItems ||
		after.QueuedBytes != before.QueuedBytes {
		t.Fatalf("oversize mutated accounting: before=%+v after=%+v", before, after)
	}
}

func TestHandlerUsesSmallestTimeoutAndShutdownCancellation(t *testing.T) {
	t.Parallel()

	t.Run("registration timeout", func(t *testing.T) {
		t.Parallel()

		options := mailboxOptions()
		options.HandlerTimeout = time.Second
		hub, err := NewHub(testLogger(), options)
		if err != nil {
			t.Fatalf("NewHub: %v", err)
		}
		var observed time.Duration
		if err := OnWithOptions(
			hub,
			"handler.test.v1",
			HandlerOptions[handlerPayload]{
				Parallelism: 1,
				Timeout:     25 * time.Millisecond,
			},
			func(ctx context.Context, _ *Conn, _ *handlerPayload) error {
				deadline, exists := ctx.Deadline()
				if !exists {
					t.Fatal("handler context has no deadline")
				}
				observed = time.Until(deadline)
				<-ctx.Done()
				return ctx.Err()
			},
		); err != nil {
			t.Fatalf("OnWithOptions: %v", err)
		}
		hub.status = hubRunning
		hub.runCtx = context.Background()
		connection := newUnitConnection(t, hub, "handler-timeout", "user:timeout")
		if err := hub.dispatchApplication(
			connection,
			handlerEnvelope("timeout", 1),
		); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if observed <= 0 || observed > 50*time.Millisecond {
			t.Fatalf("observed timeout = %s", observed)
		}
		if hub.Metrics().ActiveHandlers != 0 {
			t.Fatalf("active handlers = %d", hub.Metrics().ActiveHandlers)
		}
	})

	t.Run("hub timeout and shutdown cancellation", func(t *testing.T) {
		t.Parallel()

		options := mailboxOptions()
		options.HandlerTimeout = 500 * time.Millisecond
		hub, err := NewHub(testLogger(), options)
		if err != nil {
			t.Fatalf("NewHub: %v", err)
		}
		started := make(chan struct{})
		canceled := make(chan struct{})
		if err := OnWithOptions(
			hub,
			"handler.test.v1",
			HandlerOptions[handlerPayload]{
				Parallelism: 1,
				Timeout:     time.Second,
			},
			func(ctx context.Context, _ *Conn, _ *handlerPayload) error {
				close(started)
				<-ctx.Done()
				close(canceled)
				return ctx.Err()
			},
		); err != nil {
			t.Fatalf("OnWithOptions: %v", err)
		}
		hub.status = hubRunning
		hub.runCtx, hub.runCancel = context.WithCancel(context.Background())
		connection := newUnitConnection(t, hub, "handler-cancel", "user:cancel")
		dispatchDone := make(chan error, 1)
		go func() {
			dispatchDone <- hub.dispatchApplication(
				connection,
				handlerEnvelope("cancel", 1),
			)
		}()
		<-started
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := hub.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("handler context was not canceled")
		}
		if err := <-dispatchDone; err != nil {
			t.Fatalf("dispatch error = %v", err)
		}
	})
}
