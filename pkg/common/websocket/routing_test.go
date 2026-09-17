package websocket

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRoutingTargetsAuthenticatedPrincipalsOnly(t *testing.T) {
	t.Parallel()

	hub := newUnitHub(t, mailboxOptions())
	first := newUnitConnection(t, hub, "first", "user:1")
	second := newUnitConnection(t, hub, "second", "user:1")
	_ = first
	_ = second
	message := outboundMessage(DeliveryEphemeral, "", "targeted")
	if err := hub.SendToPrincipal(context.Background(), "user:1", message); err != nil {
		t.Fatalf("SendToPrincipal: %v", err)
	}
	if first.mailbox.items != 1 || second.mailbox.items != 1 {
		t.Fatalf("mailboxes first=%d second=%d", first.mailbox.items, second.mailbox.items)
	}
	if err := hub.SendToPrincipal(context.Background(), "user:missing", message); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("missing principal error = %v", err)
	}
}

func TestInboundRateLimitClosesRepeatedAbuse(t *testing.T) {
	t.Parallel()

	options := mailboxOptions()
	options.InboundMessagesPerSecond = 1
	options.InboundBurst = 1
	hub := newUnitHub(t, options)
	connection := newUnitConnection(t, hub, "connection-1", "user:1")
	now := timeForRateTest()
	if !connection.allowInbound(now) {
		t.Fatal("initial inbound token rejected")
	}
	if connection.allowInbound(now) {
		t.Fatal("second inbound token unexpectedly allowed")
	}
	hub.handleInboundOverload(connection)
	hub.handleInboundOverload(connection)
	hub.handleInboundOverload(connection)
	if connection.state != connectionClosing ||
		connection.closeOptions.Code != ClosePolicyViolation ||
		hub.Metrics().InboundRateLimited != 3 {
		t.Fatalf("state=%d close=%+v metrics=%+v", connection.state, connection.closeOptions, hub.Metrics())
	}
}

func timeForRateTest() time.Time {
	return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
}
