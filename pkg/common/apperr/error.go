package apperr

import "fmt"

// AppError is the custom error structure for the application
type AppError struct {
	Code       int    `json:"code"`
	Message    string `json:"message"`
	RootCause  error  `json:"-"`
	HTTPStatus int    `json:"-"`
}

// Error implements the error interface
func (e *AppError) Error() string {
	return fmt.Sprintf("Code: %d, Message: %s, RootCause: %v", e.Code, e.Message, e.RootCause)
}

// New creates a new AppError
func New(code int, message string, httpStatus int, rootCause error) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		RootCause:  rootCause,
	}
}

// Wrap returns a new AppError wrapping an existing error
func Wrap(err error, code int, message string, httpStatus int) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		RootCause:  err,
	}
}