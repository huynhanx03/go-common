package websocket

import "errors"

var (
	ErrInvalidOptions      = errors.New("websocket: invalid options")
	ErrInvalidEnvelope     = errors.New("websocket: invalid envelope")
	ErrUnsupportedVersion  = errors.New("websocket: unsupported protocol version")
	ErrUnknownOperation    = errors.New("websocket: unknown operation")
	ErrMessageTooLarge     = errors.New("websocket: message too large")
	ErrInvalidMessage      = errors.New("websocket: invalid message")
	ErrUnauthorized        = errors.New("websocket: unauthorized")
	ErrForbiddenTopic      = errors.New("websocket: topic forbidden")
	ErrNotFound            = errors.New("websocket: destination not found")
	ErrOverloaded          = errors.New("websocket: overloaded")
	ErrClosed              = errors.New("websocket: closed")
	ErrShuttingDown        = errors.New("websocket: shutting down")
	ErrInvalidSelector     = errors.New("websocket: invalid connection selector")
	ErrInvalidCloseOptions = errors.New("websocket: invalid close options")
	ErrUndrainedHandlers   = errors.New("websocket: handlers did not drain")
)

type UndrainedError struct {
	Handlers int
}

func (err *UndrainedError) Error() string {
	return ErrUndrainedHandlers.Error()
}

func (err *UndrainedError) Unwrap() error {
	return ErrUndrainedHandlers
}

type ErrorCode string

const (
	CodeInvalidMessage     ErrorCode = "invalid_message"
	CodeUnsupportedVersion ErrorCode = "unsupported_version"
	CodeUnknownOperation   ErrorCode = "unknown_operation"
	CodeMessageTooLarge    ErrorCode = "message_too_large"
	CodeUnauthorized       ErrorCode = "unauthorized"
	CodeForbiddenTopic     ErrorCode = "forbidden_topic"
	CodeNotFound           ErrorCode = "not_found"
	CodeOverloaded         ErrorCode = "overloaded"
	CodeShuttingDown       ErrorCode = "shutting_down"
	CodeInternal           ErrorCode = "internal"
)

// ProtocolError carries only a stable machine code and retryability flag. Its
// cause supports errors.Is inside the server but is never rendered on wire.
type ProtocolError struct {
	Code      ErrorCode
	Retryable bool
	cause     error
}

func (err *ProtocolError) Error() string {
	if err == nil {
		return "websocket: protocol error"
	}
	return "websocket: " + string(err.Code)
}

func (err *ProtocolError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func newProtocolError(cause error, code ErrorCode, retryable bool) error {
	return &ProtocolError{Code: code, Retryable: retryable, cause: cause}
}
