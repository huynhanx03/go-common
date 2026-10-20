package websocket

import (
	"context"
	"testing"
)

func benchmarkHubAndConnection(b *testing.B) (*Hub, *Conn) {
	b.Helper()

	options := validHubOptions()
	options.WriteQueueCapacity = maxWriteQueueCapacity
	options.WriteQueueByteCapacity = maxWriteQueueBytes
	options.MaxHubQueuedBytes = maxWriteQueueBytes
	options.MaxOutboundMessageBytes = DefaultMessageBytes
	hub, err := NewHub(options)
	if err != nil {
		b.Fatalf("NewHub: %v", err)
	}
	hub.status = hubRunning
	hub.runCtx = context.Background()
	ctx, cancel := context.WithCancel(context.Background())
	connection := &Conn{
		hub:           hub,
		id:            "benchmark-connection",
		principal:     authenticatedPrincipal("user:benchmark"),
		remoteIP:      "192.0.2.1",
		ctx:           ctx,
		cancel:        cancel,
		state:         connectionOpen,
		topics:        map[string]struct{}{"benchmark": {}},
		closeOptions:  DefaultCloseOptions(),
		mailbox:       newMailbox(),
		handlerSlots:  make(chan struct{}, hub.options.MaxInFlightHandlersPerConnection),
		handlerStates: make(map[string]*connectionHandlerState),
	}
	hub.state.connections[connection.id] = connection
	addIndexedConnection(hub.state.topics, "benchmark", connection)
	addIndexedConnection(hub.state.subjects, connection.principal.Subject, connection)
	addIndexedConnection(hub.state.remoteIPs, connection.remoteIP, connection)
	b.Cleanup(cancel)
	return hub, connection
}

func BenchmarkBroadcast(b *testing.B) {
	hub, _ := benchmarkHubAndConnection(b)
	message := outboundMessage(DeliveryLatestState, "benchmark-state", "value")
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := hub.Broadcast(ctx, "benchmark", message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMailbox(b *testing.B) {
	hub, connection := benchmarkHubAndConnection(b)
	frame, err := encodeOutboundMessage(
		hub,
		outboundMessage(DeliveryLatestState, "benchmark-state", "value"),
	)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := hub.offerFrame(
			connection,
			frame,
			DeliveryLatestState,
			"benchmark-state",
		); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHandler(b *testing.B) {
	hub, connection := benchmarkHubAndConnection(b)
	hub.status = hubNew
	if err := On(hub, "handler.test.v1", func(
		context.Context,
		*Conn,
		*handlerPayload,
	) error {
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	hub.status = hubRunning
	envelope := handlerEnvelope("benchmark", 42)

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := hub.dispatchApplication(connection, envelope); err != nil {
			b.Fatal(err)
		}
	}
}
