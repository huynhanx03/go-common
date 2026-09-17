package request

import "errors"

var (
	ErrInvalidPolicy         = errors.New("http request: invalid retry policy")
	ErrInvalidRequest        = errors.New("http request: invalid request")
	ErrInvalidIdempotencyKey = errors.New("http request: invalid idempotency key")
	ErrUnsafeRetry           = errors.New("http request: unsafe retry rejected")
	ErrBodyNotReplayable     = errors.New("http request: body is not replayable")
	ErrNoResult              = errors.New("http request: client returned no response or error")
	ErrInvalidFanout         = errors.New("http request: invalid fanout")
	ErrInvalidTask           = errors.New("http request: invalid fanout task")
	ErrTaskPanic             = errors.New("http request: fanout task panic")
	ErrNoSuccessfulResult    = errors.New("http request: no successful fanout result")
)

// TaskPanicError is deliberately redacted; a recovered panic value may contain
// credentials or caller-owned data.
type TaskPanicError struct{}

func (*TaskPanicError) Error() string { return ErrTaskPanic.Error() }
func (*TaskPanicError) Unwrap() error { return ErrTaskPanic }
