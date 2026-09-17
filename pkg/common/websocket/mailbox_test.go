package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func mailboxOptions() Options {
	options := validHubOptions()
	options.WriteQueueCapacity = 1
	options.WriteQueueByteCapacity = 512
	options.MaxHubQueuedBytes = 1024
	options.MaxOutboundMessageBytes = 256
	return options
}

func newUnitConnection(
	t *testing.T,
	hub *Hub,
	id string,
	subject string,
) *Conn {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	principal := authenticatedPrincipal(subject)
	connection := &Conn{
		hub:           hub,
		id:            id,
		principal:     principal,
		remoteIP:      "192.0.2.1",
		ctx:           ctx,
		cancel:        cancel,
		state:         connectionOpen,
		topics:        make(map[string]struct{}),
		closeOptions:  DefaultCloseOptions(),
		mailbox:       newMailbox(),
		handlerSlots:  make(chan struct{}, hub.options.MaxInFlightHandlersPerConnection),
		handlerStates: make(map[string]*connectionHandlerState),
	}
	hub.state.connections[id] = connection
	addIndexedConnection(hub.state.subjects, subject, connection)
	addIndexedConnection(hub.state.remoteIPs, connection.remoteIP, connection)
	t.Cleanup(cancel)
	return connection
}

func newUnitHub(t *testing.T, options Options) *Hub {
	t.Helper()

	hub, err := NewHub(options)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	hub.status = hubRunning
	hub.runCtx = context.Background()
	return hub
}

func outboundMessage(class DeliveryClass, key, value string) Message {
	return Message{
		Envelope: Envelope{
			Version:   ProtocolVersion,
			Operation: OperationEvent,
			ID:        "state-event",
			Type:      "test.state.v1",
			Data:      json.RawMessage(`{"value":"` + value + `"}`),
		},
		Class:       class,
		CoalesceKey: key,
	}
}

func TestMailboxLatestStateCoalescesAndOwnsEncodedFrame(t *testing.T) {
	t.Parallel()

	hub := newUnitHub(t, mailboxOptions())
	connection := newUnitConnection(t, hub, "connection-1", "user:1")
	first := outboundMessage(DeliveryLatestState, "state:1", "first")
	if err := hub.SendToConnection(context.Background(), connection.id, first); err != nil {
		t.Fatalf("first SendToConnection: %v", err)
	}
	first.Envelope.Data[2] = 'X'
	second := outboundMessage(DeliveryLatestState, "state:1", "second")
	if err := hub.SendToConnection(context.Background(), connection.id, second); err != nil {
		t.Fatalf("second SendToConnection: %v", err)
	}

	snapshot := hub.Metrics()
	if snapshot.QueuedItems != 1 || snapshot.Coalesced != 1 || snapshot.QueuedBytes <= 0 {
		t.Fatalf("metrics = %+v", snapshot)
	}
	frame, ok := hub.dequeue(connection)
	if !ok {
		t.Fatal("mailbox was empty")
	}
	if !bytes.Contains(frame.payload, []byte(`"second"`)) ||
		bytes.Contains(frame.payload, []byte(`"Xirst"`)) {
		t.Fatalf("retained payload = %s", frame.payload)
	}
	snapshot = hub.Metrics()
	if snapshot.QueuedItems != 0 || snapshot.QueuedBytes != 0 {
		t.Fatalf("metrics after dequeue = %+v", snapshot)
	}
}

func TestMailboxBackpressureClassesHaveIndependentOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("ephemeral and latest drop", func(t *testing.T) {
		t.Parallel()

		hub := newUnitHub(t, mailboxOptions())
		connection := newUnitConnection(t, hub, "connection-1", "user:1")
		if err := hub.SendToConnection(
			context.Background(),
			connection.id,
			outboundMessage(DeliveryEphemeral, "", "occupy"),
		); err != nil {
			t.Fatalf("occupy: %v", err)
		}
		if err := hub.SendToConnection(
			context.Background(),
			connection.id,
			outboundMessage(DeliveryEphemeral, "", "drop"),
		); err != nil {
			t.Fatalf("ephemeral drop: %v", err)
		}
		if err := hub.SendToConnection(
			context.Background(),
			connection.id,
			outboundMessage(DeliveryLatestState, "new-key", "drop"),
		); err != nil {
			t.Fatalf("latest drop: %v", err)
		}
		snapshot := hub.Metrics()
		if snapshot.DroppedEphemeral != 1 ||
			snapshot.DroppedLatest != 1 ||
			connection.state != connectionOpen {
			t.Fatalf("metrics=%+v state=%d", snapshot, connection.state)
		}
	})

	t.Run("critical closes and releases all charges", func(t *testing.T) {
		t.Parallel()

		hub := newUnitHub(t, mailboxOptions())
		connection := newUnitConnection(t, hub, "connection-1", "user:1")
		if err := hub.SendToConnection(
			context.Background(),
			connection.id,
			outboundMessage(DeliveryEphemeral, "", "occupy"),
		); err != nil {
			t.Fatalf("occupy: %v", err)
		}
		if err := hub.SendToConnection(
			context.Background(),
			connection.id,
			outboundMessage(DeliveryCritical, "", "critical"),
		); err != nil {
			t.Fatalf("critical outcome must remain destination-local: %v", err)
		}
		snapshot := hub.Metrics()
		if snapshot.CriticalClosed != 1 ||
			snapshot.QueuedItems != 0 ||
			snapshot.QueuedBytes != 0 ||
			connection.state != connectionClosing ||
			connection.closeOptions.Code != CloseTryAgainLater {
			t.Fatalf("metrics=%+v state=%d close=%+v", snapshot, connection.state, connection.closeOptions)
		}
	})
}

func TestBroadcastPressureOnOneDestinationDoesNotFailOthers(t *testing.T) {
	t.Parallel()

	options := mailboxOptions()
	options.WriteQueueCapacity = 2
	hub := newUnitHub(t, options)
	slow := newUnitConnection(t, hub, "slow", "user:slow")
	healthy := newUnitConnection(t, hub, "healthy", "user:healthy")
	for _, connection := range []*Conn{slow, healthy} {
		connection.topics["stream:1"] = struct{}{}
		addIndexedConnection(hub.state.topics, "stream:1", connection)
	}
	for index := 0; index < options.WriteQueueCapacity; index++ {
		if err := hub.SendToConnection(
			context.Background(),
			slow.id,
			outboundMessage(DeliveryEphemeral, "", "occupy"),
		); err != nil {
			t.Fatalf("occupy slow[%d]: %v", index, err)
		}
	}
	if err := hub.Broadcast(
		context.Background(),
		"stream:1",
		outboundMessage(DeliveryEphemeral, "", "broadcast"),
	); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if slow.mailbox.items != options.WriteQueueCapacity ||
		healthy.mailbox.items != 1 ||
		hub.Metrics().DroppedEphemeral != 1 {
		t.Fatalf(
			"slow=%d healthy=%d metrics=%+v",
			slow.mailbox.items,
			healthy.mailbox.items,
			hub.Metrics(),
		)
	}

	if err := hub.Broadcast(
		context.Background(),
		"no-subscribers",
		outboundMessage(DeliveryEphemeral, "", "no-op"),
	); err != nil {
		t.Fatalf("zero-subscriber Broadcast: %v", err)
	}
}

func TestMailboxContextAndTargetValidation(t *testing.T) {
	t.Parallel()

	hub := newUnitHub(t, mailboxOptions())
	connection := newUnitConnection(t, hub, "connection-1", "user:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := hub.SendToConnection(
		ctx,
		connection.id,
		outboundMessage(DeliveryEphemeral, "", "value"),
	); err != context.Canceled {
		t.Fatalf("canceled error = %v", err)
	}
	if err := hub.SendToConnection(
		context.Background(),
		"missing",
		outboundMessage(DeliveryEphemeral, "", "value"),
	); err != ErrNotFound {
		t.Fatalf("missing error = %v", err)
	}

	anonymous := &Conn{
		hub:       hub,
		id:        "anonymous",
		principal: authentication.Anonymous(),
	}
	hub.state.connections[anonymous.id] = anonymous
	if err := hub.SendToPrincipal(
		context.Background(),
		"",
		outboundMessage(DeliveryEphemeral, "", "value"),
	); err != ErrInvalidMessage {
		t.Fatalf("anonymous group error = %v", err)
	}
}

func TestMailboxDrainDoesNotRetainCharges(t *testing.T) {
	t.Parallel()

	hub := newUnitHub(t, mailboxOptions())
	connection := newUnitConnection(t, hub, "connection-1", "user:1")
	if err := hub.SendToConnection(
		context.Background(),
		connection.id,
		outboundMessage(DeliveryEphemeral, "", "value"),
	); err != nil {
		t.Fatalf("SendToConnection: %v", err)
	}
	hub.mu.Lock()
	hub.clearMailboxLocked(connection)
	hub.mu.Unlock()
	waitForCondition(t, func() bool {
		snapshot := hub.Metrics()
		return snapshot.QueuedItems == 0 && snapshot.QueuedBytes == 0
	})
	if connection.mailbox.items != 0 || connection.mailbox.bytes != 0 {
		t.Fatalf("mailbox items=%d bytes=%d", connection.mailbox.items, connection.mailbox.bytes)
	}

}
