package kafka

const (
	// HeaderRequestID is the header key for request ID
	HeaderRequestID = "X-Request-ID"
	// HeaderTraceID is the header key for trace ID
	HeaderTraceID = "X-Trace-ID"
)

// ContextKey is the type for context keys to avoid collisions
type ContextKey string

const (
	// ContextKeyRequestID is the context key for request ID
	ContextKeyRequestID ContextKey = "request_id"

	// ContextKeyTraceID is the context key for trace ID
	ContextKeyTraceID   ContextKey = "trace_id"
)
