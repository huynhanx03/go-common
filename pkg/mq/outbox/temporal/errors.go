package temporaloutbox

import (
	"errors"

	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

var (
	ErrInvalidRoutes      = errors.New("invalid Temporal outbox routes")
	ErrRouteNotConfigured = errors.New("Temporal outbox route not configured")
	ErrInvalidMessage     = outbox.ErrInvalidMessage
)
