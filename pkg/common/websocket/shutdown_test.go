package websocket

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type recordingTransport struct {
	readStarted chan struct{}
	closeCalled chan CloseOptions
	closeOnce   sync.Once
}

func newRecordingTransport() *recordingTransport {
	return &recordingTransport{
		readStarted: make(chan struct{}),
		closeCalled: make(chan CloseOptions, 1),
	}
}

func (*recordingTransport) SetReadLimit(int64) {}

func (transport *recordingTransport) Read(ctx context.Context) ([]byte, bool, error) {
	transport.closeOnce.Do(func() { close(transport.readStarted) })
	<-ctx.Done()
	return nil, false, ctx.Err()
}

func (*recordingTransport) Write(context.Context, []byte) error { return nil }
func (*recordingTransport) Ping(context.Context) error          { return nil }
func (transport *recordingTransport) Close(code int, reason string) error {
	transport.closeCalled <- CloseOptions{Code: code, Reason: reason}
	return nil
}
func (*recordingTransport) CloseNow() error { return nil }

func TestShutdownDrainsMailboxAndClosesGoingAway(t *testing.T) {
	t.Parallel()

	hub, err := NewHub(mailboxOptions())
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	hub.status = hubRunning
	hub.runCtx, hub.runCancel = context.WithCancel(context.Background())
	transport := newRecordingTransport()
	if err := hub.attachConnection(
		context.Background(),
		authenticatedPrincipal("user:1"),
		"192.0.2.1",
		func(context.Context, authentication.Principal, string) error { return nil },
		transport,
	); err != nil {
		t.Fatalf("attachConnection: %v", err)
	}
	<-transport.readStarted

	hub.mu.RLock()
	var connection *Conn
	for _, current := range hub.state.connections {
		connection = current
	}
	hub.mu.RUnlock()
	if connection == nil {
		t.Fatal("connection missing")
	}
	if err := hub.SendToConnection(
		context.Background(),
		connection.id,
		outboundMessage(DeliveryCritical, "", "final"),
	); err != nil {
		t.Fatalf("SendToConnection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case options := <-transport.closeCalled:
		if options.Code != CloseGoingAway {
			t.Fatalf("close options = %+v", options)
		}
	case <-time.After(time.Second):
		t.Fatal("transport was not closed")
	}
	snapshot := hub.Metrics()
	if snapshot.QueuedItems != 0 || snapshot.QueuedBytes != 0 {
		t.Fatalf("metrics after shutdown = %+v", snapshot)
	}
}
