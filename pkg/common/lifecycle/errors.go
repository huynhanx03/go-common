package lifecycle

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidOptions     = errors.New("lifecycle: invalid options")
	ErrInvalidComponent   = errors.New("lifecycle: invalid component")
	ErrDuplicateComponent = errors.New("lifecycle: duplicate component")
	ErrInvalidState       = errors.New("lifecycle: invalid state")
	ErrUnexpectedExit     = errors.New("lifecycle: required component exited unexpectedly")
	ErrComponentPanic     = errors.New("lifecycle: component panic")
)

// ComponentError identifies which lifecycle boundary failed while preserving
// the original error for errors.Is/errors.As.
type ComponentError struct {
	Component string
	Operation string
	Err       error
}

func (err *ComponentError) Error() string {
	if err == nil {
		return "lifecycle: component error"
	}
	return fmt.Sprintf("lifecycle: component %q %s: %v", err.Component, err.Operation, err.Err)
}

func (err *ComponentError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}
