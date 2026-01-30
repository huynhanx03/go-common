package apperr

import (
	"fmt"
)

// Generic Action Messages
const (
	MsgCreateFailed  = "failed to create"
	MsgGetFailed     = "failed to get"
	MsgUpdateFailed  = "failed to update"
	MsgDeleteFailed  = "failed to delete"
	MsgCheckFailed   = "failed to check"
	MsgFoundFailed   = "failed to find"
	MsgSaveFailed    = "failed to save"
	MsgGenFailed     = "failed to generate"
	MsgProcessFailed = "failed to process"
	MsgNotFound      = "not found"
	MsgDatabaseError = "database error"
)

// MapError wraps an error with a standardized message"
func MapError(serviceName string, err error, code int, msg string, httpStatus int) *AppError {
	if err == nil {
		return nil
	}

	formattedMsg := fmt.Sprintf("%s %s", serviceName, msg)
	return Wrap(err, code, formattedMsg, httpStatus)
}

// NewError creates a new AppError with standardized message format
func NewError(serviceName string, code int, msg string, httpStatus int, cause error) *AppError {
	formattedMsg := fmt.Sprintf("%s %s", serviceName, msg)
	return New(code, formattedMsg, httpStatus, cause)
}
