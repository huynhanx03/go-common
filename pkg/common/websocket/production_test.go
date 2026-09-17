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

func (*orderedTransport) Ping(context.Context) error { return nil }

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
		hub, err := NewHub(options)
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
		hub, err := NewHub(options)
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
