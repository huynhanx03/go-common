package apperr

import "fmt"

// AppError is the custom error structure for the application.
// HTTPStatus is derived from Code via response.GetHTTPCode().
type AppError struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RootCause error  `json:"-"`
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.RootCause != nil {
		return fmt.Sprintf("code=%d msg=%s cause=%v", e.Code, e.Message, e.RootCause)
	}
	return fmt.Sprintf("code=%d msg=%s", e.Code, e.Message)
}

// Unwrap returns the root cause for errors.Is/As chain.
func (e *AppError) Unwrap() error {
	return e.RootCause
}

// New creates a new AppError. HTTPStatus is derived from Code automatically.
func New(code int, message string, cause error) *AppError {
	return &AppError{
		Code:      code,
		Message:   message,
		RootCause: cause,
	}
}
