package websocket

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type connectionTransport interface {
	SetReadLimit(int64)
	Read(context.Context) ([]byte, bool, error)
	Write(context.Context, []byte) error
	Ping(context.Context) error
	Close(int, string) error
	CloseNow() error
}

type connectionState uint8

const (
	connectionOpen connectionState = iota + 1
	connectionClosing
	connectionClosed
)

type Conn struct {
	hub       *Hub
	id        string
	principal authentication.Principal
	remoteIP  string
	transport connectionTransport
	authorize TopicAuthorizer

	ctx    context.Context
	cancel context.CancelFunc

	state        connectionState     // guarded by Hub.mu
	topics       map[string]struct{} // guarded by Hub.mu
	closeOptions CloseOptions        // guarded by Hub.mu
	draining     bool                // guarded by Hub.mu
	mailbox      *mailbox
	pumpsDone    atomic.Int32

	inboundTokens        float64
	inboundLast          time.Time
	inboundOverloadCount int

	handlerSlots  chan struct{}
	handlerMu     sync.Mutex
	handlerStates map[string]*connectionHandlerState
}

func (connection *Conn) ID() string {
	if connection == nil {
		return ""
	}
	return connection.id
}

func (connection *Conn) Principal() authentication.Principal {
	if connection == nil {
		return authentication.Anonymous()
	}
	return connection.principal
}

func (connection *Conn) Send(ctx context.Context, message Message) error {
	if connection == nil || connection.hub == nil {
		return ErrClosed
	}
	return connection.hub.sendToConnection(ctx, connection, message)
}

func (connection *Conn) Close(ctx context.Context, options CloseOptions) error {
	if connection == nil || connection.hub == nil {
		return ErrClosed
	}
	result, err := connection.hub.CloseConnections(
		ctx,
		ConnectionSelector{ConnectionIDs: []string{connection.id}},
		options,
	)
	if err != nil {
		return err
	}
	if result.Matched == 0 {
		return ErrNotFound
	}
	return nil
}
