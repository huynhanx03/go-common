// Package internal hides the selected WebSocket engine from the public API.
package internal

import (
	"context"
	"net/http"

	coderwebsocket "github.com/coder/websocket"
)

type Connection struct {
	inner *coderwebsocket.Conn
}

func Accept(
	writer http.ResponseWriter,
	request *http.Request,
) (*Connection, error) {
	connection, err := coderwebsocket.Accept(
		writer,
		request,
		&coderwebsocket.AcceptOptions{
			// The parent package has already performed exact full-origin
			// matching. Coder's pattern matcher is intentionally bypassed so
			// it cannot widen that policy through wildcard semantics.
			InsecureSkipVerify: true,
			CompressionMode:    coderwebsocket.CompressionDisabled,
		},
	)
	if err != nil {
		return nil, err
	}
	return &Connection{inner: connection}, nil
}

func (connection *Connection) SetReadLimit(limit int64) {
	connection.inner.SetReadLimit(limit)
}

func (connection *Connection) Read(ctx context.Context) ([]byte, bool, error) {
	messageType, payload, err := connection.inner.Read(ctx)
	return payload, messageType == coderwebsocket.MessageText, err
}

func (connection *Connection) Write(ctx context.Context, payload []byte) error {
	return connection.inner.Write(ctx, coderwebsocket.MessageText, payload)
}

func (connection *Connection) Ping(ctx context.Context) error {
	return connection.inner.Ping(ctx)
}

func (connection *Connection) Close(code int, reason string) error {
	return connection.inner.Close(coderwebsocket.StatusCode(code), reason)
}

func (connection *Connection) CloseNow() error {
	return connection.inner.CloseNow()
}

// CloseStatus returns the peer-supplied protocol status without exposing the
// selected engine through the public websocket package.
func CloseStatus(err error) int {
	return int(coderwebsocket.CloseStatus(err))
}
