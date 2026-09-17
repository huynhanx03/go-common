package websocket

import (
	"errors"
	"sync/atomic"
)

type MetricsSnapshot struct {
	ActiveConnections  int64
	QueuedItems        int64
	QueuedBytes        int64
	Enqueued           uint64
	Dequeued           uint64
	Coalesced          uint64
	DroppedLatest      uint64
	DroppedEphemeral   uint64
	CriticalClosed     uint64
	InboundAccepted    uint64
	InboundRateLimited uint64
	InboundInvalid     uint64
	InboundErrors      uint64
	OutboundDelivered  uint64
	OutboundErrors     uint64
	HandlerPanics      uint64
	ActiveHandlers     int64
	Closes             CloseMetricsSnapshot
}

// CloseMetricsSnapshot groups protocol codes into a fixed set of operational
// classes. It never exposes custom close codes or reason strings as labels.
type CloseMetricsSnapshot struct {
	Normal     uint64
	Shutdown   uint64
	Auth       uint64
	Policy     uint64
	SlowClient uint64
	Protocol   uint64
	Internal   uint64
}

type hubMetrics struct {
	enqueued           atomic.Uint64
	dequeued           atomic.Uint64
	coalesced          atomic.Uint64
	droppedLatest      atomic.Uint64
	droppedEphemeral   atomic.Uint64
	criticalClosed     atomic.Uint64
	inboundAccepted    atomic.Uint64
	inboundRateLimited atomic.Uint64
	inboundInvalid     atomic.Uint64
	inboundErrors      atomic.Uint64
	outboundDelivered  atomic.Uint64
	outboundErrors     atomic.Uint64
	handlerPanics      atomic.Uint64
	activeHandlers     atomic.Int64
	closeNormal        atomic.Uint64
	closeShutdown      atomic.Uint64
	closeAuth          atomic.Uint64
	closePolicy        atomic.Uint64
	closeSlowClient    atomic.Uint64
	closeProtocol      atomic.Uint64
	closeInternal      atomic.Uint64
}

func (hub *Hub) Metrics() MetricsSnapshot {
	if hub == nil {
		return MetricsSnapshot{}
	}
	hub.mu.RLock()
	activeConnections := len(hub.state.connections)
	queuedItems := hub.queuedItems
	queuedBytes := hub.queuedBytes
	hub.mu.RUnlock()
	return MetricsSnapshot{
		ActiveConnections:  int64(activeConnections),
		QueuedItems:        queuedItems,
		QueuedBytes:        queuedBytes,
		Enqueued:           hub.metrics.enqueued.Load(),
		Dequeued:           hub.metrics.dequeued.Load(),
		Coalesced:          hub.metrics.coalesced.Load(),
		DroppedLatest:      hub.metrics.droppedLatest.Load(),
		DroppedEphemeral:   hub.metrics.droppedEphemeral.Load(),
		CriticalClosed:     hub.metrics.criticalClosed.Load(),
		InboundAccepted:    hub.metrics.inboundAccepted.Load(),
		InboundRateLimited: hub.metrics.inboundRateLimited.Load(),
		InboundInvalid:     hub.metrics.inboundInvalid.Load(),
		InboundErrors:      hub.metrics.inboundErrors.Load(),
		OutboundDelivered:  hub.metrics.outboundDelivered.Load(),
		OutboundErrors:     hub.metrics.outboundErrors.Load(),
		HandlerPanics:      hub.metrics.handlerPanics.Load(),
		ActiveHandlers:     hub.metrics.activeHandlers.Load(),
		Closes: CloseMetricsSnapshot{
			Normal:     hub.metrics.closeNormal.Load(),
			Shutdown:   hub.metrics.closeShutdown.Load(),
			Auth:       hub.metrics.closeAuth.Load(),
			Policy:     hub.metrics.closePolicy.Load(),
			SlowClient: hub.metrics.closeSlowClient.Load(),
			Protocol:   hub.metrics.closeProtocol.Load(),
			Internal:   hub.metrics.closeInternal.Load(),
		},
	}
}

type closeMetricClass uint8

const (
	closeMetricNormal closeMetricClass = iota + 1
	closeMetricShutdown
	closeMetricAuth
	closeMetricPolicy
	closeMetricSlowClient
	closeMetricProtocol
	closeMetricInternal
)

func classifyCloseMetric(code int) closeMetricClass {
	switch code {
	case CloseNormal:
		return closeMetricNormal
	case CloseGoingAway, CloseServiceRestart:
		return closeMetricShutdown
	case CloseAuthenticationGone:
		return closeMetricAuth
	case ClosePolicyViolation:
		return closeMetricPolicy
	case CloseTryAgainLater:
		return closeMetricSlowClient
	case CloseProtocolError, CloseUnsupportedData, CloseInvalidPayload, CloseMessageTooBig:
		return closeMetricProtocol
	case CloseInternalError, CloseBadGateway:
		return closeMetricInternal
	default:
		if code >= 3000 && code <= 4999 {
			return closeMetricPolicy
		}
		return closeMetricInternal
	}
}

func (metrics *hubMetrics) recordClose(code int) {
	switch classifyCloseMetric(code) {
	case closeMetricNormal:
		metrics.closeNormal.Add(1)
	case closeMetricShutdown:
		metrics.closeShutdown.Add(1)
	case closeMetricAuth:
		metrics.closeAuth.Add(1)
	case closeMetricPolicy:
		metrics.closePolicy.Add(1)
	case closeMetricSlowClient:
		metrics.closeSlowClient.Add(1)
	case closeMetricProtocol:
		metrics.closeProtocol.Add(1)
	case closeMetricInternal:
		metrics.closeInternal.Add(1)
	}
}

func (metrics *hubMetrics) recordInboundFailure(err error) {
	switch {
	case errors.Is(err, ErrOverloaded):
		metrics.inboundRateLimited.Add(1)
	case errors.Is(err, ErrInvalidEnvelope),
		errors.Is(err, ErrInvalidMessage),
		errors.Is(err, ErrUnsupportedVersion),
		errors.Is(err, ErrUnknownOperation),
		errors.Is(err, ErrForbiddenTopic),
		errors.Is(err, ErrUnauthorized):
		metrics.inboundInvalid.Add(1)
	default:
		metrics.inboundErrors.Add(1)
	}
}
