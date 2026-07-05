package apperr

import "fmt"

// Generic Action Messages using format templates.
// Use with fmt.Sprintf or pass object name via MapError variadic args.
const (
	MsgCreateFailed  = "failed to create %s"
	MsgGetFailed     = "failed to get %s"
	MsgUpdateFailed  = "failed to update %s"
	MsgDeleteFailed  = "failed to delete %s"
	MsgCheckFailed   = "failed to check %s"
	MsgFindFailed    = "failed to find %s"
	MsgSaveFailed    = "failed to save %s"
	MsgGenFailed     = "failed to generate %s"
	MsgProcessFailed = "failed to process %s"
	MsgNotFound      = "%s not found"
	MsgDatabaseError = "database error"
)

// MapError wraps an error into AppError with optional format args. Returns nil if err is nil.
func MapError(err error, code int, format string, args ...any) *AppError {
	if err == nil {
		return nil
	}
	return New(code, fmt.Sprintf(format, args...), err)
}
