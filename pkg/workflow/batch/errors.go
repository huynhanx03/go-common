package batch

import "errors"

var (
	ErrInvalidConfig      = errors.New("invalid batch configuration")
	ErrDefinitionConflict = errors.New("batch definition conflicts with the stored checkpoint")
	ErrConflict           = errors.New("batch checkpoint conflict")
	ErrInvalidProgress    = errors.New("invalid batch progress")
	ErrCompleted          = errors.New("batch is already completed")
)
