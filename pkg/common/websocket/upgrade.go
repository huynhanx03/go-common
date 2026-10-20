package websocket

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	wsinternal "github.com/huynhanx03/go-common/pkg/common/websocket/internal"
	"github.com/huynhanx03/go-common/pkg/correlation"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

const (
	maxForwardedForBytes = 4096
	maxForwardedHops     = 32
	upgradeBucketIdle    = 10 * time.Minute
)

type upgradeBucket struct {
	tokens   float64
	lastSeen time.Time
}

type upgradeReservation struct {
	subject  string
	remoteIP string
}

func (hub *Hub) Handler(
	resolve PrincipalResolver,
	authorize TopicAuthorizer,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if hub == nil || resolve == nil || authorize == nil || request == nil {
			http.Error(writer, "internal server error", http.StatusInternalServerError)
			return
		}
		if !hub.originAllowed(request.Header.Get("Origin")) {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return
		}
		remoteIP, err := hub.remoteIP(request)
		if err != nil {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		if err := hub.checkUpgrade(remoteIP, time.Now()); err != nil {
			if err == ErrShuttingDown || err == ErrClosed {
				http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			writer.Header().Set("Retry-After", "1")
			http.Error(writer, "too many requests", http.StatusTooManyRequests)
			return
		}

		requestContext := correlation.EnsureContext(request.Context())
		request = request.WithContext(requestContext)
		principal, err := resolvePrincipal(resolve, requestContext, request)
		if requestContext.Err() != nil {
			return
		}
		if err != nil || principal.Validate(time.Now().UTC()) != nil {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		reservation, err := hub.reserveUpgrade(principal, remoteIP)
		if err != nil {
			if err == ErrShuttingDown || err == ErrClosed {
				http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			writer.Header().Set("Retry-After", "1")
			http.Error(writer, "too many requests", http.StatusTooManyRequests)
			return
		}
		defer hub.releaseReservation(reservation)

		transport, err := wsinternal.Accept(writer, request)
		if err != nil {
			return
		}
		transport.SetReadLimit(hub.options.ReadLimit)
		if err := hub.attachConnection(
			requestContext,
			principal,
			remoteIP,
			authorize,
			transport,
		); err != nil {
			_ = transport.CloseNow()
		}
	})
}

func resolvePrincipal(
	resolve PrincipalResolver,
	ctx context.Context,
	request *http.Request,
) (principal authentication.Principal, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		stack := debug.Stack()
		if len(stack) > maxHandlerStackBytes {
			stack = stack[:maxHandlerStackBytes]
		}
		logger.FromContext(ctx).Error(
			"websocket principal resolver panic recovered",
			zap.String("panic_type", fmt.Sprintf("%T", recovered)),
			zap.ByteString("stack", stack),
		)
		principal = authentication.Anonymous()
		err = ErrUnauthorized
	}()
	return resolve(ctx, request)
}

func (hub *Hub) originAllowed(origin string) bool {
	normalized, err := normalizeOrigin(origin)
	if err != nil {
		return false
	}
	_, allowed := hub.allowedOrigins[normalized]
	return allowed
}

func (hub *Hub) checkUpgrade(remoteIP string, now time.Time) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.status != hubRunning {
		return ErrShuttingDown
	}
	bucket := hub.upgradeBuckets[remoteIP]
	if bucket == nil {
		if len(hub.upgradeBuckets) >= hub.maxUpgradeBuckets {
			for key, existing := range hub.upgradeBuckets {
				if now.Sub(existing.lastSeen) >= upgradeBucketIdle {
					delete(hub.upgradeBuckets, key)
				}
			}
		}
		if len(hub.upgradeBuckets) >= hub.maxUpgradeBuckets {
			return ErrOverloaded
		}
		bucket = &upgradeBucket{
			tokens:   float64(hub.options.UpgradeBurstPerRemoteIP),
			lastSeen: now,
		}
		hub.upgradeBuckets[remoteIP] = bucket
	}
	elapsed := now.Sub(bucket.lastSeen).Seconds()
	if elapsed > 0 {
		bucket.tokens = min(
			float64(hub.options.UpgradeBurstPerRemoteIP),
			bucket.tokens+elapsed*float64(hub.options.UpgradeRatePerSecondPerRemoteIP),
		)
	}
	bucket.lastSeen = now
	if bucket.tokens < 1 {
		return ErrOverloaded
	}
	bucket.tokens--
	return nil
}

func (hub *Hub) reserveUpgrade(
	principal authentication.Principal,
	remoteIP string,
) (upgradeReservation, error) {
	reservation := upgradeReservation{remoteIP: remoteIP}
	if principal.Authenticated {
		reservation.subject = principal.Subject
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.status != hubRunning {
		return upgradeReservation{}, ErrShuttingDown
	}
	total := len(hub.state.connections) +
		len(hub.state.closing) +
		hub.pendingConnections
	if total >= hub.options.MaxConnections {
		return upgradeReservation{}, ErrOverloaded
	}
	remoteCount := len(hub.state.remoteIPs[remoteIP]) + hub.pendingRemoteIPs[remoteIP]
	if remoteCount >= hub.options.MaxConnectionsPerRemoteIP {
		return upgradeReservation{}, ErrOverloaded
	}
	if principal.Authenticated {
		subjectCount := len(hub.state.subjects[principal.Subject]) +
			hub.pendingSubjects[principal.Subject]
		if subjectCount >= hub.options.MaxConnectionsPerPrincipal {
			return upgradeReservation{}, ErrOverloaded
		}
		hub.pendingSubjects[principal.Subject]++
	}
	hub.pendingConnections++
	hub.pendingRemoteIPs[remoteIP]++
	return reservation, nil
}

func (hub *Hub) releaseReservation(reservation upgradeReservation) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.pendingConnections > 0 {
		hub.pendingConnections--
	}
	decrementPending(hub.pendingRemoteIPs, reservation.remoteIP)
	if reservation.subject != "" {
		decrementPending(hub.pendingSubjects, reservation.subject)
	}
}

func decrementPending(index map[string]int, key string) {
	if index[key] <= 1 {
		delete(index, key)
		return
	}
	index[key]--
}

func (hub *Hub) attachConnection(
	requestContext context.Context,
	principal authentication.Principal,
	remoteIP string,
	authorize TopicAuthorizer,
	transport connectionTransport,
) error {
	hub.mu.Lock()
	if hub.status != hubRunning {
		hub.mu.Unlock()
		return ErrShuttingDown
	}
	connectionID := correlation.New()
	connectionContext := hub.runCtx
	connectionContext = correlation.WithContext(
		connectionContext,
		correlation.FromContext(requestContext),
	)
	connectionContext = logger.InheritContext(connectionContext, requestContext)
	connectionContext = authentication.WithPrincipal(connectionContext, principal)
	connectionContext, cancel := context.WithCancel(connectionContext)
	connectionContext = logger.WithFields(
		connectionContext,
		logger.String("connection_id", connectionID),
		logger.String("authenticated", strconv.FormatBool(principal.Authenticated)),
	)
	connection := &Conn{
		hub:           hub,
		id:            connectionID,
		principal:     principal,
		remoteIP:      remoteIP,
		transport:     transport,
		authorize:     authorize,
		ctx:           connectionContext,
		cancel:        cancel,
		state:         connectionOpen,
		topics:        make(map[string]struct{}),
		closeOptions:  DefaultCloseOptions(),
		mailbox:       newMailbox(),
		inboundTokens: float64(hub.options.InboundBurst),
		inboundLast:   time.Now(),
		handlerSlots:  make(chan struct{}, hub.options.MaxInFlightHandlersPerConnection),
		handlerStates: make(map[string]*connectionHandlerState),
	}
	hub.state.connections[connectionID] = connection
	addIndexedConnection(hub.state.remoteIPs, remoteIP, connection)
	if principal.Authenticated {
		addIndexedConnection(hub.state.subjects, principal.Subject, connection)
	}
	hub.pumps.Add(2)
	hub.mu.Unlock()

	logger.FromContext(connectionContext).Info("websocket connection established")
	go hub.readPump(connection)
	go hub.writePump(connection)
	return nil
}

func addIndexedConnection(
	index map[string]map[string]*Conn,
	key string,
	connection *Conn,
) {
	connections := index[key]
	if connections == nil {
		connections = make(map[string]*Conn)
		index[key] = connections
	}
	connections[connection.id] = connection
}

func (hub *Hub) remoteIP(request *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return "", err
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "", err
	}
	peer = peer.Unmap()
	if !hub.trustedProxy(peer) {
		return peer.String(), nil
	}
	raw := request.Header.Get("X-Forwarded-For")
	if raw == "" || len(raw) > maxForwardedForBytes {
		return peer.String(), nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxForwardedHops {
		return peer.String(), nil
	}
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address, parseError := netip.ParseAddr(strings.TrimSpace(part))
		if parseError != nil {
			return peer.String(), nil
		}
		chain = append(chain, address.Unmap())
	}
	current := peer
	for index := len(chain) - 1; index >= 0; index-- {
		if !hub.trustedProxy(current) {
			break
		}
		current = chain[index]
	}
	return current.String(), nil
}

func (hub *Hub) trustedProxy(address netip.Addr) bool {
	for _, prefix := range hub.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
