package outbox

import (
	"context"
	"sort"
	"strings"

	"github.com/huynhanx03/go-common/pkg/logger"
)

const (
	maximumRouterHandlers    = 1024
	maximumHandlerNameBytes  = 64
	failureRouteNotFound     = "route_not_found"
	failureRouteHandlerPanic = "route_handler_panic"
)

// HandlerFunc consumes one immutable message. Handlers registered on the same
// destination run in declaration order and must be idempotent because a later
// handler failure causes the whole message to be retried.
type HandlerFunc func(context.Context, Message) error

// Route binds one named handler to one destination. Repeating a destination
// intentionally creates a deterministic fan-out; repeating a handler name for
// the same destination is rejected.
type Route struct {
	Destination string
	HandlerName string
	Handle      HandlerFunc
}

type routedHandler struct {
	name   string
	handle HandlerFunc
}

// Router is an immutable, in-process Publisher. It lets Relay retain all
// durable retry, backoff, dead-letter, fencing, and lifecycle semantics while
// applications provide typed local consumers without a second broker.
type Router struct {
	routes       map[string][]routedHandler
	destinations []string
}

// NewRouter validates and freezes a route catalog. The input may be discarded
// or mutated after this call without changing the router.
func NewRouter(routes ...Route) (*Router, error) {
	if len(routes) == 0 || len(routes) > maximumRouterHandlers {
		return nil, ErrInvalidRouter
	}
	catalog := make(map[string][]routedHandler)
	names := make(map[string]map[string]struct{})
	for _, route := range routes {
		destination := strings.Clone(route.Destination)
		handlerName := strings.Clone(route.HandlerName)
		if !validBoundedText(destination, maximumDestinationBytes) ||
			!validBoundedText(handlerName, maximumHandlerNameBytes) ||
			route.Handle == nil {
			return nil, ErrInvalidRouter
		}
		if _, exists := names[destination]; !exists {
			names[destination] = make(map[string]struct{})
		}
		if _, duplicate := names[destination][handlerName]; duplicate {
			return nil, ErrInvalidRouter
		}
		names[destination][handlerName] = struct{}{}
		catalog[destination] = append(catalog[destination], routedHandler{
			name:   handlerName,
			handle: route.Handle,
		})
	}
	if len(catalog) > maximumClaimDestinations {
		return nil, ErrInvalidRouter
	}
	destinations := make([]string, 0, len(catalog))
	for destination := range catalog {
		destinations = append(destinations, destination)
	}
	sort.Strings(destinations)
	return &Router{routes: catalog, destinations: destinations}, nil
}

// Destinations returns a stable copy suitable for Options.Destinations when a
// relay must claim only this router's closed catalog.
func (router *Router) Destinations() []string {
	if router == nil {
		return nil
	}
	return append([]string(nil), router.destinations...)
}

// Publish fans one message out to every registered handler in deterministic
// order. A missing route is a permanent failure so an all-destination relay
// quarantines new event types instead of silently losing or indefinitely
// retaining them.
func (router *Router) Publish(ctx context.Context, message Message) (PublishAck, error) {
	if router == nil || ctx == nil {
		return PublishAck{}, permanentRouterFailure(failureRouteNotFound)
	}
	handlers, exists := router.routes[message.Destination]
	if !exists || len(handlers) == 0 {
		return PublishAck{}, permanentRouterFailure(failureRouteNotFound)
	}
	for _, handler := range handlers {
		handlerCtx := logger.WithFields(ctx, logger.String("event_handler", handler.name))
		logger.FromContext(handlerCtx).Debug("outbox route handler started")
		if err := callRouteHandler(handlerCtx, handler, cloneRouterMessage(message)); err != nil {
			logger.FromContext(handlerCtx).Warn("outbox route handler failed")
			return PublishAck{}, err
		}
		logger.FromContext(handlerCtx).Debug("outbox route handler completed")
	}
	return PublishAck{Reference: strings.Clone(message.ID)}, nil
}

func callRouteHandler(
	ctx context.Context,
	handler routedHandler,
	message Message,
) (err error) {
	defer func() {
		if recover() != nil {
			err = &PublishError{
				Code:      failureRouteHandlerPanic,
				Retryable: true,
				Ambiguous: true,
			}
		}
	}()
	return handler.handle(ctx, message)
}

func permanentRouterFailure(code string) error {
	return &PublishError{Code: code, Retryable: false, Ambiguous: false}
}

func cloneRouterMessage(message Message) Message {
	message.ID = strings.Clone(message.ID)
	message.Destination = strings.Clone(message.Destination)
	message.Key = append([]byte(nil), message.Key...)
	message.Payload = append([]byte(nil), message.Payload...)
	message.Metadata.CorrelationID = strings.Clone(message.Metadata.CorrelationID)
	message.Metadata.Attributes = append([]Attribute(nil), message.Metadata.Attributes...)
	for index := range message.Metadata.Attributes {
		message.Metadata.Attributes[index].Key = strings.Clone(message.Metadata.Attributes[index].Key)
		message.Metadata.Attributes[index].Value = strings.Clone(message.Metadata.Attributes[index].Value)
	}
	return message
}

var _ Publisher = (*Router)(nil)
