package temporal

import "errors"

var (
	ErrInvalidConfig              = errors.New("invalid Temporal configuration")
	ErrInvalidTLSConfig           = errors.New("invalid TLS configuration")
	ErrDialFailed                 = errors.New("Temporal connection failed")
	ErrDuplicateContextPropagator = errors.New("duplicate context propagator")
	ErrWorkerStartFailed          = errors.New("Temporal worker failed to start")
	ErrInvalidWorkerState         = errors.New("invalid Temporal worker state")
	ErrInvalidBatchWorkflowInput  = errors.New("invalid batch workflow input")
	ErrBatchHeartbeatBudget       = errors.New("batch activity heartbeat timeout is too short")
)
