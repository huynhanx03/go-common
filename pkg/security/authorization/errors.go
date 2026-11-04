package authorization

import "errors"

var (
	ErrInvalidRequest = errors.New("invalid request")
	ErrEvaluation     = errors.New("evaluation failed")
)
