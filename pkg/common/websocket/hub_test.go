package websocket

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	coderwebsocket "github.com/coder/websocket"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func TestSubscribeAlwaysAuthorizesAndUnsubscribeOnlyRemovesCaller(t *testing.T) {
	var authorizationMu sync.Mutex
	authorizationCalls := make(map[string]int)
	harness := startRunningHub(
		t,
		validHubOptions(),
		func(context.Context, *http.Request) (authentication.Principal, error) {
			return authenticatedPrincipal("user:42"), nil
		},
		func(_ context.Context, _ authentication.Principal, topic string) error {
			authorizationMu.Lock()
			defer authorizationMu.Unlock()
			authorizationCalls[topic]++
			if topic == "resource:denied" {
				return errors.New("policy database secret")
			}
			return nil
		},
	)

	first, _, err := dialHub(
		context.Background(),
		harness.server.URL,
		"https://app.example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	defer first.CloseNow()
	second, _, err := dialHub(
		context.Background(),
		harness.server.URL,
		"https://app.example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("second Dial: %v", err)
	}
	defer second.CloseNow()

	sendControlEnvelope(t, first, OperationSubscribe, "resource:denied")
	sendControlEnvelope(t, first, OperationSubscribe, "resource:allowed")
	sendControlEnvelope(t, second, OperationSubscribe, "resource:allowed")
	waitForCondition(t, func() bool {
		authorizationMu.Lock()
		defer authorizationMu.Unlock()
		return authorizationCalls["resource:denied"] == 1 &&
			authorizationCalls["resource:allowed"] == 2
	})
	waitForCondition(t, func() bool {
		harness.hub.mu.RLock()
		defer harness.hub.mu.RUnlock()
		return len(harness.hub.state.topics["resource:allowed"]) == 2 &&
			len(harness.hub.state.topics["resource:denied"]) == 0
	})

	sendControlEnvelope(t, first, OperationUnsubscribe, "resource:allowed")
	waitForCondition(t, func() bool {
		harness.hub.mu.RLock()
		defer harness.hub.mu.RUnlock()
		return len(harness.hub.state.topics["resource:allowed"]) == 1
	})
	authorizationMu.Lock()
	defer authorizationMu.Unlock()
	if authorizationCalls["resource:allowed"] != 2 {
		t.Fatalf("unsubscribe called authorizer: %+v", authorizationCalls)
	}

	if _, exists := reflect.TypeOf((*Conn)(nil)).MethodByName("Subscribe"); exists {
		t.Fatal("Conn exposes a public Subscribe authorization bypass")
	}
}

func sendControlEnvelope(
	t *testing.T,
	connection *coderwebsocket.Conn,
	operation string,
	topic string,
) {
	t.Helper()

	frame, err := EncodeEnvelope(Envelope{
		Version:   ProtocolVersion,
		Operation: operation,
		ID:        "subscription-change",
		Topic:     topic,
		Type:      "protocol.subscription.v1",
	}, DefaultMessageBytes)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := connection.Write(ctx, coderwebsocket.MessageText, frame); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestHubRunAndShutdownStateContracts(t *testing.T) {
	t.Parallel()

	if _, err := NewHub(Options{}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("NewHub zero options error = %v", err)
	}
	hub, err := NewHub(validHubOptions())
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	if err := hub.Run(nil); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("Run(nil) error = %v", err)
	}

	runContext, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- hub.Run(runContext)
	}()
	select {
	case <-hub.started:
	case <-time.After(time.Second):
		t.Fatal("hub did not start")
	}
	if err := hub.Run(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Run error = %v", err)
	}
	cancel()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := hub.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := hub.Shutdown(shutdownContext); err != nil {
		t.Fatalf("repeated Shutdown: %v", err)
	}
	select {
	case err := <-runErrors:
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}
