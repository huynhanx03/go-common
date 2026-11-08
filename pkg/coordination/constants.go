package coordination

import "time"

const (
	defaultLeaseTTL              = 30 * time.Second
	defaultLeaseOperationTimeout = 3 * time.Second
	defaultLeaseCleanupTimeout   = 3 * time.Second
	defaultBackoffInitialDelay   = 100 * time.Millisecond
	defaultBackoffMaxDelay       = 30 * time.Second
	defaultBackoffMultiplier     = 2.0
	leaseOwnerBytes              = 16
	maxDurationMilliseconds      = int64((1<<63 - 1) / int64(time.Millisecond))
)
