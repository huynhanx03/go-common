package settings

// Coordination configures lease ownership and contention behavior.
// Durations use milliseconds so callers can tune short leases without losing precision.
// Zero values use the consuming client's defaults, except AcquireTimeoutMilliseconds,
// where zero means acquisition returns immediately when the resource is held.
type Coordination struct {
	TTLMilliseconds              int     `mapstructure:"ttl_milliseconds"`
	RenewIntervalMilliseconds    int     `mapstructure:"renew_interval_milliseconds"`
	OperationTimeoutMilliseconds int     `mapstructure:"operation_timeout_milliseconds"`
	CleanupTimeoutMilliseconds   int     `mapstructure:"cleanup_timeout_milliseconds"`
	AcquireTimeoutMilliseconds   int     `mapstructure:"acquire_timeout_milliseconds"`
	Backoff                      Backoff `mapstructure:"backoff"`
}
