package logger

const (
	fieldTimestamp      = "timestamp"
	fieldLevel          = "level"
	fieldService        = "service"
	fieldVersion        = "version"
	fieldEnvironment    = "environment"
	fieldComponent      = "component"
	fieldMessage        = "message"
	fieldRequestID      = "request_id"
	fieldCorrelation    = "correlation_id"
	fieldTraceID        = "trace_id"
	fieldSpanID         = "span_id"
	fieldDurationMS     = "duration_ms"
	fieldMetadata       = "metadata"
	responseUnavailable = "logger unavailable"

	defaultDevelopmentLevel   = "debug"
	defaultProductionLevel    = "info"
	defaultMaxSize            = 100
	defaultMaxBackups         = 5
	defaultMaxAge             = 30
	defaultSamplingInitial    = 100
	defaultSamplingThereafter = 100

	maxLoggerPathBytes       = 4096
	maxLoggerMetadataBytes   = 256
	maxLoggerVersionBytes    = 128
	maxRotationSizeMegabytes = 1 << 20
	maxRotationBackups       = 10_000
	maxRotationAgeDays       = 100 * 365
	maxSamplingEntries       = 10_000_000
	maxRequestIDBytes        = 128
	maxContextFields         = 16
	maxFieldKeyBytes         = 64
	maxFieldValueBytes       = 1024

	RequestIDHeader = "X-Request-ID"
)
