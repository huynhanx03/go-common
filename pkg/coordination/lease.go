package coordination

import (
	"context"
	"errors"
	"sync"
)

// Lease represents temporary ownership of a resource.
type Lease interface {
	Renew(context.Context) error
	Release(context.Context) error
}

// FencedLease is a lease with a token that downstream writes can validate.
type FencedLease interface {
	Lease
	Grant() Grant
}

// Adapter acquires advisory leases.
type Adapter interface {
	TryAcquire(context.Context, AcquireRequest) (Lease, error)
}

// FencedAdapter acquires leases with ordered fencing grants.
type FencedAdapter interface {
	TryAcquireFenced(context.Context, AcquireRequest) (FencedLease, error)
}

type leaseHandle struct {
	lease Lease
	core  *clientCore

	mu       sync.Mutex
	released bool
	lost     error
}

type fencedLeaseHandle struct {
	*leaseHandle
	grant Grant
}

func (lease *fencedLeaseHandle) Grant() Grant { return lease.grant }

// TryAcquire attempts one advisory lease acquisition without waiting.
func (c *Client) TryAcquire(ctx context.Context, resource string) (Lease, error) {
	lease, _, err := c.core.tryAcquire(ctx, resource)
	return lease, err
}

// TryAcquire attempts one fenced lease acquisition without waiting.
func (c *FencedClient) TryAcquire(ctx context.Context, resource string) (FencedLease, error) {
	lease, grant, err := c.core.tryAcquire(ctx, resource)
	if err != nil {
		return nil, err
	}
	return &fencedLeaseHandle{leaseHandle: lease, grant: grant}, nil
}

// Do holds an advisory lease while fn runs, renewing it until fn returns.
func (c *Client) Do(ctx context.Context, resource string, fn func(context.Context) error) error {
	if fn == nil {
		return ErrInvalidConfig
	}
	return c.core.do(ctx, resource, func(ctx context.Context, _ Grant) error { return fn(ctx) })
}

// Do holds a fenced lease while fn runs, renewing it until fn returns.
func (c *FencedClient) Do(ctx context.Context, resource string, fn func(context.Context, Grant) error) error {
	if fn == nil {
		return ErrInvalidConfig
	}
	return c.core.do(ctx, resource, fn)
}

func (lease *leaseHandle) Renew(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return ErrLeaseLost
	}
	if lease.lost != nil {
		return lease.lost
	}

	operationCtx, cancel := context.WithTimeout(ctx, lease.core.config.operationTimeout)
	err := lease.lease.Renew(operationCtx)
	operationErr := operationCtx.Err()
	cancel()
	if err == nil {
		err = operationErr
	}
	if err != nil {
		lease.lost = errors.Join(ErrLeaseLost, err)
		return lease.lost
	}
	return nil
}

func (lease *leaseHandle) Release(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil
	}
	lease.released = true
	return lease.core.releaseRaw(ctx, lease.lease)
}
