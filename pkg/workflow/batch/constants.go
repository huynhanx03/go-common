package batch

import "time"

const (
	Ready     Status = "ready"
	Running   Status = "running"
	Completed Status = "completed"

	defaultMaxPages         = 100
	defaultMaxCursorBytes   = 64 * 1024
	defaultMaxBoundaryBytes = 64 * 1024
	defaultPageTimeout      = 2 * time.Second
)
