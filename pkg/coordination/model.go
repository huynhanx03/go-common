package coordination

import "time"

// FenceToken identifies an ordered grant issued by one authority.
type FenceToken struct {
	Authority string
	Value     int64
}

// Grant binds a lease owner and resource to its fencing token.
type Grant struct {
	Resource string
	Owner    string
	Fence    FenceToken
}

// AcquireRequest contains the identity and lifetime requested from an adapter.
type AcquireRequest struct {
	Resource string
	Owner    string
	TTL      time.Duration
}
