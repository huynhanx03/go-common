package websocket

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/logger"
)

type hubStatus uint8

const (
	hubNew hubStatus = iota + 1
	hubRunning
	hubClosing
	hubClosed
)

type hubState struct {
	connections map[string]*Conn
	closing     map[string]*Conn
	subjects    map[string]map[string]*Conn
	topics      map[string]map[string]*Conn
	remoteIPs   map[string]map[string]*Conn
}

type Hub struct {
	options        Options
	allowedOrigins map[string]struct{}
	trustedProxies []netip.Prefix

	mu     sync.RWMutex
	status hubStatus
	state  hubState

	pendingConnections int
	pendingSubjects    map[string]int
	pendingRemoteIPs   map[string]int
	upgradeBuckets     map[string]*upgradeBucket
	maxUpgradeBuckets  int

	runCtx    context.Context
	runCancel context.CancelFunc
	started   chan struct{}
	done      chan struct{}

	shutdownOnce sync.Once
	pumps        sync.WaitGroup
	handlers     sync.WaitGroup

	shutdownDeadline time.Time
	queuedItems      int64
	queuedBytes      int64
	metrics          hubMetrics

	globalHandlerSlots chan struct{}
	handlerRegistry    map[string]*registeredHandler
}

func NewHub(options Options) (*Hub, error) {
	normalized, err := NormalizeOptions(options)
	if err != nil {
		return nil, err
	}
	trustedProxies, err := parseTrustedProxies(normalized.TrustedProxies)
	if err != nil {
		return nil, err
	}
	origins := make(map[string]struct{}, len(normalized.AllowedOrigins))
	for _, origin := range normalized.AllowedOrigins {
		origins[origin] = struct{}{}
	}
	return &Hub{
		options:        normalized,
		allowedOrigins: origins,
		trustedProxies: trustedProxies,
		status:         hubNew,
		state: hubState{
			connections: make(map[string]*Conn),
			closing:     make(map[string]*Conn),
			subjects:    make(map[string]map[string]*Conn),
			topics:      make(map[string]map[string]*Conn),
			remoteIPs:   make(map[string]map[string]*Conn),
		},
		pendingSubjects:    make(map[string]int),
		pendingRemoteIPs:   make(map[string]int),
		upgradeBuckets:     make(map[string]*upgradeBucket),
		maxUpgradeBuckets:  min(100_000, max(1024, normalized.MaxConnections*2)),
		started:            make(chan struct{}),
		done:               make(chan struct{}),
		globalHandlerSlots: make(chan struct{}, normalized.MaxInFlightHandlers),
		handlerRegistry:    make(map[string]*registeredHandler),
	}, nil
}

func (hub *Hub) Run(ctx context.Context) error {
	if hub == nil || ctx == nil {
		return ErrInvalidOptions
	}
	hub.mu.Lock()
	if hub.status != hubNew {
		hub.mu.Unlock()
		return ErrClosed
	}
	hub.runCtx, hub.runCancel = context.WithCancel(ctx)
	hub.status = hubRunning
	close(hub.started)
	runCtx := hub.runCtx
	hub.mu.Unlock()

	<-runCtx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), hub.options.ShutdownTimeout)
	defer cancel()
	err := hub.Shutdown(shutdownCtx)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func (hub *Hub) Shutdown(ctx context.Context) error {
	if hub == nil || ctx == nil {
		return ErrInvalidOptions
	}
	hub.shutdownOnce.Do(func() {
		hub.beginShutdown(ctx)
	})
	select {
	case <-hub.done:
		return nil
	case <-ctx.Done():
		if handlers := hub.metrics.activeHandlers.Load(); handlers > 0 {
			return &UndrainedError{Handlers: int(handlers)}
		}
		return ctx.Err()
	}
}

func (hub *Hub) beginShutdown(ctx context.Context) {
	hub.mu.Lock()
	if hub.status == hubNew {
		hub.status = hubClosed
		close(hub.started)
		hub.mu.Unlock()
		close(hub.done)
		return
	}
	if hub.status == hubClosed {
		hub.mu.Unlock()
		close(hub.done)
		return
	}
	hub.status = hubClosing
	hub.shutdownDeadline = time.Now().Add(hub.options.ShutdownTimeout)
	if deadline, exists := ctx.Deadline(); exists && deadline.Before(hub.shutdownDeadline) {
		hub.shutdownDeadline = deadline
	}
	if hub.runCancel != nil {
		hub.runCancel()
	}
	for _, connection := range hub.state.connections {
		hub.markDrainingLocked(connection, CloseOptions{
			Code:   CloseGoingAway,
			Reason: "server shutting down",
		})
	}
	hub.mu.Unlock()

	go func() {
		hub.pumps.Wait()
		hub.handlers.Wait()
		hub.mu.Lock()
		hub.status = hubClosed
		hub.mu.Unlock()
		close(hub.done)
	}()
}

func (hub *Hub) markClosingLocked(connection *Conn, options CloseOptions) {
	hub.transitionClosingLocked(connection, options, false)
}

func (hub *Hub) markDrainingLocked(connection *Conn, options CloseOptions) {
	hub.transitionClosingLocked(connection, options, true)
}

func (hub *Hub) transitionClosingLocked(
	connection *Conn,
	options CloseOptions,
	drain bool,
) {
	if connection == nil || connection.state != connectionOpen {
		return
	}
	hub.metrics.recordClose(options.Code)
	connection.state = connectionClosing
	connection.closeOptions = options
	connection.draining = drain
	delete(hub.state.connections, connection.id)
	hub.state.closing[connection.id] = connection
	removeIndexedConnection(hub.state.remoteIPs, connection.remoteIP, connection.id)
	if connection.principal.Authenticated {
		removeIndexedConnection(
			hub.state.subjects,
			connection.principal.Subject,
			connection.id,
		)
	}
	for topic := range connection.topics {
		removeIndexedConnection(hub.state.topics, topic, connection.id)
	}
	if !drain {
		hub.clearMailboxLocked(connection)
	}
	connection.cancel()
}

func removeIndexedConnection(
	index map[string]map[string]*Conn,
	key string,
	connectionID string,
) {
	connections := index[key]
	delete(connections, connectionID)
	if len(connections) == 0 {
		delete(index, key)
	}
}

func (hub *Hub) finalizeConnection(connection *Conn) {
	hub.mu.Lock()
	closeCode := connection.closeOptions.Code
	draining := connection.draining
	connection.state = connectionClosed
	delete(hub.state.connections, connection.id)
	delete(hub.state.closing, connection.id)
	removeIndexedConnection(hub.state.remoteIPs, connection.remoteIP, connection.id)
	if connection.principal.Authenticated {
		removeIndexedConnection(
			hub.state.subjects,
			connection.principal.Subject,
			connection.id,
		)
	}
	for topic := range connection.topics {
		removeIndexedConnection(hub.state.topics, topic, connection.id)
	}
	hub.clearMailboxLocked(connection)
	hub.mu.Unlock()

	logContext := logger.WithFields(
		connection.ctx,
		logger.String("close_code", strconv.Itoa(closeCode)),
		logger.String("draining", strconv.FormatBool(draining)),
	)
	logger.FromContext(logContext).Info("websocket connection closed")
}
