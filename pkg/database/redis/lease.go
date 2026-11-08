package redis

import (
	"context"
	"errors"
	"time"

	"github.com/bsm/redislock"

	"github.com/huynhanx03/go-common/pkg/coordination"
)

type leaseAdapter struct {
	engine *RedisEngine
	client *redislock.Client
}

type redisLease struct {
	lock *redislock.Lock
	ttl  time.Duration
}

// NewLeaseAdapter adapts the engine's Redis connection and namespace to advisory leases.
func NewLeaseAdapter(engine *RedisEngine) coordination.Adapter {
	return &leaseAdapter{engine: engine, client: redislock.New(engine.client)}
}

func (adapter *leaseAdapter) TryAcquire(ctx context.Context, request coordination.AcquireRequest) (coordination.Lease, error) {
	if request.Resource == "" || request.Owner == "" || request.TTL.Milliseconds() <= 0 {
		return nil, ErrInvalidConfig
	}
	lock, err := adapter.client.Obtain(ctx, adapter.engine.scopedKey(request.Resource), request.TTL, &redislock.Options{
		RetryStrategy: redislock.NoRetry(),
		Token:         request.Owner,
	})
	if err != nil {
		if errors.Is(err, redislock.ErrNotObtained) {
			return nil, errors.Join(coordination.ErrHeld, err)
		}
		return nil, err
	}
	return &redisLease{lock: lock, ttl: request.TTL}, nil
}

func (lease *redisLease) Renew(ctx context.Context) error {
	err := lease.lock.Refresh(ctx, lease.ttl, &redislock.Options{RetryStrategy: redislock.NoRetry()})
	if err == nil {
		return nil
	}
	if errors.Is(err, redislock.ErrNotObtained) || errors.Is(err, redislock.ErrLockNotHeld) {
		return errors.Join(coordination.ErrLeaseLost, err)
	}
	return errors.Join(coordination.ErrAmbiguous, err)
}

func (lease *redisLease) Release(ctx context.Context) error {
	err := lease.lock.Release(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, redislock.ErrNotObtained) || errors.Is(err, redislock.ErrLockNotHeld) {
		return errors.Join(coordination.ErrLeaseLost, err)
	}
	return errors.Join(coordination.ErrAmbiguous, err)
}
