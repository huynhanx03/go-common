package temporal

import "time"

const (
	// BatchProgressQueryName identifies the batch workflow progress query.
	BatchProgressQueryName = "batch_progress"

	defaultShutdownTimeout = 30 * time.Second
	maximumTemporalTimeout = 24 * time.Hour
	tracerName             = "go-common/temporal"

	requestIDHeader             = "request_id"
	correlationIDHeader         = "correlation_id"
	maxPropagatedValueBytes     = 2 * 1024
	workflowIDField             = "workflow_id"
	runIDField                  = "run_id"
	activityIDField             = "activity_id"
	activityAttemptField        = "attempt"
	batchWorkflowInputErrorType = "batch_workflow_input"
	batchPermanentErrorType     = "batch_permanent"
)
