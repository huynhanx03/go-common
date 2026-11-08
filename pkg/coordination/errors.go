package coordination

import "errors"

var (
	ErrInvalidConfig     = errors.New("invalid coordination configuration")
	ErrHeld              = errors.New("resource is already held")
	ErrLeaseLost         = errors.New("lease ownership lost")
	ErrAmbiguous         = errors.New("lease operation outcome is ambiguous")
	ErrInvalidFence      = errors.New("invalid fencing grant")
	ErrAuthorityMismatch = errors.New("fence authority does not match")
	ErrStaleFence        = errors.New("fence is older than the accepted value")
)
