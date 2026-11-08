package etcd

import "time"

const (
	ownershipKeyPrefix    = "/go-common/coordination/leases"
	leaseAuthority        = "etcd"
	compareEqual          = "="
	defaultCleanupTimeout = 3 * time.Second
)
