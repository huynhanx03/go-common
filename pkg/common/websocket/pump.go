package websocket

import (
	"context"
	"time"

	wsinternal "github.com/huynhanx03/go-common/pkg/common/websocket/internal"
)

const maxConsecutiveInboundOverloads = 3

func (hub *Hub) readPump(connection *Conn) {
	defer hub.pumpFinished(connection)
	for {
		payload, text, err := connection.transport.Read(connection.ctx)
		if err != nil {
			hub.closeAfterPumpError(connection, err)
			return
		}
		if !text {
			hub.metrics.inboundInvalid.Add(1)
			hub.mu.Lock()
			hub.markClosingLocked(connection, CloseOptions{
				Code:   CloseUnsupportedData,
				Reason: "text messages required",
			})
			hub.mu.Unlock()
			return
		}
		if !connection.allowInbound(time.Now()) {
			hub.handleInboundOverload(connection)
			continue
		}
		envelope, err := DecodeEnvelope(payload, hub.options.ReadLimit)
		if err != nil {
			hub.metrics.recordInboundFailure(err)
			hub.enqueueProtocolError(connection, "", err)
			continue
		}
		switch envelope.Operation {
		case OperationSubscribe, OperationUnsubscribe, OperationPing, OperationPong:
			if err := hub.handleControl(connection, envelope); err != nil {
				hub.metrics.recordInboundFailure(err)
			} else {
				hub.metrics.inboundAccepted.Add(1)
			}
		case OperationEvent, OperationAck:
			if err := hub.dispatchApplication(connection, envelope); err != nil {
				hub.metrics.recordInboundFailure(err)
				hub.enqueueProtocolError(connection, envelope.ID, err)
			} else {
				hub.metrics.inboundAccepted.Add(1)
			}
		default:
			hub.metrics.inboundInvalid.Add(1)
			hub.enqueueProtocolError(connection, envelope.ID, ErrUnknownOperation)
		}
	}
}

func (connection *Conn) allowInbound(now time.Time) bool {
	if connection.inboundLast.IsZero() {
		connection.inboundLast = now
		connection.inboundTokens = float64(connection.hub.options.InboundBurst)
	}
	elapsed := now.Sub(connection.inboundLast).Seconds()
	if elapsed > 0 {
		connection.inboundTokens = min(
			float64(connection.hub.options.InboundBurst),
			connection.inboundTokens+
				elapsed*float64(connection.hub.options.InboundMessagesPerSecond),
		)
	}
	connection.inboundLast = now
	if connection.inboundTokens < 1 {
		return false
	}
	connection.inboundTokens--
	connection.inboundOverloadCount = 0
	return true
}

func (hub *Hub) handleInboundOverload(connection *Conn) {
	hub.metrics.inboundRateLimited.Add(1)
	connection.inboundOverloadCount++
	if connection.inboundOverloadCount == 1 {
		hub.enqueueProtocolError(connection, "", ErrOverloaded)
	}
	if connection.inboundOverloadCount < maxConsecutiveInboundOverloads {
		return
	}
	hub.mu.Lock()
	hub.markClosingLocked(connection, CloseOptions{
		Code:   ClosePolicyViolation,
		Reason: "inbound rate exceeded",
	})
	hub.mu.Unlock()
}

func (hub *Hub) writePump(connection *Conn) {
	defer hub.pumpFinished(connection)
	pingTicker := time.NewTicker(hub.options.PingInterval)
	defer pingTicker.Stop()

	var authenticationExpiry <-chan time.Time
	var authenticationTimer *time.Timer
	if connection.principal.Authenticated {
		delay := time.Until(connection.principal.AuthenticatedUntil)
		if delay < 0 {
			delay = 0
		}
		authenticationTimer = time.NewTimer(delay)
		authenticationExpiry = authenticationTimer.C
		defer authenticationTimer.Stop()
	}

	for {
		select {
		case <-authenticationExpiry:
			hub.expireAuthentication(connection)
		default:
		}
		if connection.ctx.Err() != nil {
			hub.closeTransport(connection)
			return
		}

		select {
		case <-pingTicker.C:
			ctx, cancel := context.WithTimeout(
				connection.ctx,
				hub.options.PongTimeout,
			)
			err := connection.transport.Ping(ctx)
			cancel()
			if err != nil {
				hub.metrics.outboundErrors.Add(1)
				hub.closeAfterPumpError(connection, err)
				hub.closeTransport(connection)
				return
			}
		default:
		}

		if frame, available := hub.dequeue(connection); available {
			ctx, cancel, writable := hub.writeContext(connection)
			if !writable {
				hub.discardAndClose(connection)
				return
			}
			err := connection.transport.Write(ctx, frame.payload)
			cancel()
			if err != nil {
				hub.metrics.outboundErrors.Add(1)
				hub.discardAndClose(connection)
				return
			}
			hub.metrics.outboundDelivered.Add(1)
			continue
		}

		if connection.ctx.Err() != nil {
			hub.closeTransport(connection)
			return
		}
		select {
		case <-connection.mailbox.wake:
		case <-pingTicker.C:
			ctx, cancel := context.WithTimeout(
				connection.ctx,
				hub.options.PongTimeout,
			)
			err := connection.transport.Ping(ctx)
			cancel()
			if err != nil {
				hub.metrics.outboundErrors.Add(1)
				hub.closeAfterPumpError(connection, err)
				hub.closeTransport(connection)
				return
			}
		case <-authenticationExpiry:
			hub.expireAuthentication(connection)
		case <-connection.ctx.Done():
		}
	}
}

func (hub *Hub) expireAuthentication(connection *Conn) {
	hub.mu.Lock()
	hub.markClosingLocked(connection, CloseOptions{
		Code:   CloseAuthenticationGone,
		Reason: "authentication expired",
	})
	hub.mu.Unlock()
}

func (hub *Hub) writeContext(
	connection *Conn,
) (context.Context, context.CancelFunc, bool) {
	hub.mu.RLock()
	draining := connection.draining
	deadline := hub.shutdownDeadline
	hub.mu.RUnlock()
	if !draining {
		if connection.ctx.Err() != nil {
			return nil, func() {}, false
		}
		ctx, cancel := context.WithTimeout(
			connection.ctx,
			hub.options.WriteTimeout,
		)
		return ctx, cancel, true
	}
	writeDeadline := time.Now().Add(hub.options.WriteTimeout)
	if !deadline.IsZero() && deadline.Before(writeDeadline) {
		writeDeadline = deadline
	}
	if !writeDeadline.After(time.Now()) {
		return nil, func() {}, false
	}
	ctx, cancel := context.WithDeadline(context.Background(), writeDeadline)
	return ctx, cancel, true
}

func (hub *Hub) discardAndClose(connection *Conn) {
	hub.mu.Lock()
	if connection.state == connectionOpen {
		hub.markClosingLocked(connection, CloseOptions{
			Code:   CloseInternalError,
			Reason: "write failed",
		})
	}
	hub.clearMailboxLocked(connection)
	hub.mu.Unlock()
	hub.closeTransport(connection)
}

func (hub *Hub) closeTransport(connection *Conn) {
	hub.mu.RLock()
	options := connection.closeOptions
	hub.mu.RUnlock()
	_ = connection.transport.Close(options.Code, options.Reason)
}

func (hub *Hub) closeAfterPumpError(connection *Conn, cause error) {
	code := wsinternal.CloseStatus(cause)
	options := CloseOptions{Code: code, Reason: "connection closed"}
	if validateCloseOptions(options) != nil {
		options.Code = CloseInternalError
	}
	hub.mu.Lock()
	hub.markClosingLocked(connection, options)
	hub.mu.Unlock()
}

func (hub *Hub) pumpFinished(connection *Conn) {
	if connection.pumpsDone.Add(1) == 2 {
		hub.finalizeConnection(connection)
	}
	hub.pumps.Done()
}
