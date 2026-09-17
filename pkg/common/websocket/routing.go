package websocket

import "context"

func (hub *Hub) Broadcast(
	ctx context.Context,
	topic string,
	message Message,
) error {
	if hub == nil || ctx == nil || !validRequiredTerm(topic, maxTopicBytes) {
		return ErrInvalidMessage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if message.Envelope.Topic == "" {
		message.Envelope.Topic = topic
	} else if message.Envelope.Topic != topic {
		return ErrInvalidMessage
	}
	frame, err := encodeOutboundMessage(hub, message)
	if err != nil {
		return err
	}

	hub.mu.RLock()
	if hub.status != hubRunning {
		hub.mu.RUnlock()
		return ErrShuttingDown
	}
	destinations := make([]*Conn, 0, len(hub.state.topics[topic]))
	for _, connection := range hub.state.topics[topic] {
		destinations = append(destinations, connection)
	}
	hub.mu.RUnlock()

	for _, connection := range destinations {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = hub.offerFrame(
			connection,
			frame,
			message.Class,
			message.CoalesceKey,
		)
	}
	return nil
}

func (hub *Hub) SendToConnection(
	ctx context.Context,
	connectionID string,
	message Message,
) error {
	if hub == nil || ctx == nil || !validRequiredTerm(connectionID, maxIDBytes) {
		return ErrInvalidMessage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	frame, err := encodeOutboundMessage(hub, message)
	if err != nil {
		return err
	}
	hub.mu.RLock()
	connection := hub.state.connections[connectionID]
	hub.mu.RUnlock()
	if connection == nil {
		return ErrNotFound
	}
	return hub.offerFrame(
		connection,
		frame,
		message.Class,
		message.CoalesceKey,
	)
}

func (hub *Hub) SendToPrincipal(
	ctx context.Context,
	subject string,
	message Message,
) error {
	if hub == nil || ctx == nil || !validRequiredTerm(subject, maxSelectorBytes) {
		return ErrInvalidMessage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	frame, err := encodeOutboundMessage(hub, message)
	if err != nil {
		return err
	}
	hub.mu.RLock()
	if hub.status != hubRunning {
		hub.mu.RUnlock()
		return ErrShuttingDown
	}
	destinations := make([]*Conn, 0, len(hub.state.subjects[subject]))
	for _, connection := range hub.state.subjects[subject] {
		destinations = append(destinations, connection)
	}
	hub.mu.RUnlock()
	if len(destinations) == 0 {
		return ErrNotFound
	}
	for _, connection := range destinations {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = hub.offerFrame(
			connection,
			frame,
			message.Class,
			message.CoalesceKey,
		)
	}
	return nil
}

func (hub *Hub) sendToConnection(
	ctx context.Context,
	connection *Conn,
	message Message,
) error {
	if hub == nil || connection == nil || ctx == nil {
		return ErrInvalidMessage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	frame, err := encodeOutboundMessage(hub, message)
	if err != nil {
		return err
	}
	return hub.offerFrame(
		connection,
		frame,
		message.Class,
		message.CoalesceKey,
	)
}

func encodeOutboundMessage(hub *Hub, message Message) (*encodedFrame, error) {
	encoded, err := EncodeMessage(message, hub.options.MaxOutboundMessageBytes)
	if err != nil {
		return nil, err
	}
	return &encodedFrame{payload: encoded}, nil
}
