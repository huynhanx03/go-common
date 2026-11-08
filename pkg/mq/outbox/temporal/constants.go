package temporaloutbox

const (
	CodeInvalidMessage      = "invalid_message"
	CodeRouteNotConfigured  = "route_not_configured"
	CodeWorkflowStart       = "workflow_start"
	CodeWorkflowStartDenied = "workflow_start_denied"

	maximumRoutes          = 1024
	maximumRouteTextBytes  = 512
	maximumWorkflowIDBytes = 1000
)
