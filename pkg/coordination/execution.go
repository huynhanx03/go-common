package coordination

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

type callbackResult struct {
	err   error
	panic any
}

func (c *clientCore) do(ctx context.Context, resource string, fn func(context.Context, Grant) error) error {
	if ctx == nil || resource == "" {
		return ErrInvalidConfig
	}
	owner, err := newOwner()
	if err != nil {
		return err
	}
	lease, grant, err := c.acquireUntil(ctx, resource, owner)
	if err != nil {
		return err
	}
	return c.runWithLease(ctx, lease, grant, fn)
}

func (c *clientCore) tryAcquire(ctx context.Context, resource string) (*leaseHandle, Grant, error) {
	if ctx == nil || resource == "" {
		return nil, Grant{}, ErrInvalidConfig
	}
	owner, err := newOwner()
	if err != nil {
		return nil, Grant{}, err
	}
	return c.acquireOnce(ctx, resource, owner)
}

func (c *clientCore) acquireUntil(ctx context.Context, resource, owner string) (*leaseHandle, Grant, error) {
	if c.config.acquireTimeout == 0 {
		return c.acquireOnce(ctx, resource, owner)
	}
	acquireCtx, cancel := context.WithTimeout(ctx, c.config.acquireTimeout)
	defer cancel()

	for attempt := 0; ; attempt++ {
		lease, grant, err := c.acquireOnce(acquireCtx, resource, owner)
		if err == nil || !errors.Is(err, ErrHeld) {
			return lease, grant, err
		}
		if acquireCtx.Err() != nil {
			return nil, Grant{}, errors.Join(err, acquireCtx.Err())
		}
		if err := waitForRetry(acquireCtx, c.config.backoff.Delay(attempt)); err != nil {
			return nil, Grant{}, errors.Join(err, acquireCtx.Err())
		}
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay < 0 {
		return ErrInvalidConfig
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *clientCore) acquireOnce(ctx context.Context, resource, owner string) (*leaseHandle, Grant, error) {
	if err := ctx.Err(); err != nil {
		return nil, Grant{}, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, c.config.operationTimeout)
	lease, grant, err := c.acquire(operationCtx, AcquireRequest{Resource: resource, Owner: owner, TTL: c.config.ttl})
	operationErr := operationCtx.Err()
	cancel()
	if err != nil {
		if lease != nil {
			err = errors.Join(err, c.releaseRaw(ctx, lease))
		}
		if ctx.Err() != nil {
			return nil, Grant{}, errors.Join(err, ctx.Err())
		}
		return nil, Grant{}, err
	}
	if lease == nil {
		return nil, Grant{}, ErrInvalidConfig
	}
	if ctx.Err() != nil || operationErr != nil {
		cleanupErr := c.releaseRaw(ctx, lease)
		return nil, Grant{}, errors.Join(ErrAmbiguous, ctx.Err(), operationErr, cleanupErr)
	}
	if c.fenced {
		if err := validateGrant(resource, owner, grant); err != nil {
			return nil, Grant{}, errors.Join(err, c.releaseRaw(ctx, lease))
		}
	}
	return &leaseHandle{lease: lease, core: c}, grant, nil
}

func validateGrant(resource, owner string, grant Grant) error {
	if grant.Resource != resource || grant.Owner != owner || grant.Fence.Authority == "" || grant.Fence.Value <= 0 {
		return ErrInvalidFence
	}
	return nil
}

func (c *clientCore) runWithLease(ctx context.Context, lease *leaseHandle, grant Grant, fn func(context.Context, Grant) error) error {
	workCtx, cancelWork := context.WithCancel(ctx)
	stopRenew := make(chan struct{})
	renewDone := make(chan struct{})
	lost := make(chan error, 1)
	go c.renewLoop(ctx, cancelWork, stopRenew, renewDone, lost, lease)

	callbackDone := make(chan callbackResult, 1)
	start := make(chan struct{})
	go runCallback(workCtx, start, lost, grant, fn, callbackDone)

	var result callbackResult
	var workErr error
	receivedResult := false
	select {
	case <-ctx.Done():
		workErr = ctx.Err()
		cancelWork()
	case err := <-lost:
		workErr = err
		cancelWork()
	default:
		close(start)
	}
	if workErr == nil {
		select {
		case result = <-callbackDone:
			receivedResult = true
			workErr = result.err
		case <-ctx.Done():
			workErr = ctx.Err()
			cancelWork()
		case err := <-lost:
			workErr = err
			cancelWork()
		}
	}
	if !receivedResult {
		result = <-callbackDone
		workErr = errors.Join(workErr, result.err)
	}

	close(stopRenew)
	<-renewDone
	select {
	case renewErr := <-lost:
		workErr = errors.Join(workErr, renewErr)
	default:
	}
	workErr = errors.Join(workErr, ctx.Err())
	cancelWork()
	releaseErr := lease.Release(ctx)
	if result.panic != nil {
		if releaseErr != nil {
			c.log.Error(ctx, "lease cleanup failed", zap.Error(releaseErr))
		}
		panic(result.panic)
	}
	return errors.Join(workErr, releaseErr)
}

func runCallback(ctx context.Context, start <-chan struct{}, lost <-chan error, grant Grant, fn func(context.Context, Grant) error, done chan<- callbackResult) {
	select {
	case <-ctx.Done():
		done <- callbackResult{err: ctx.Err()}
		return
	case <-start:
	}
	if err := ctx.Err(); err != nil {
		done <- callbackResult{err: err}
		return
	}
	select {
	case err := <-lost:
		done <- callbackResult{err: err}
		return
	default:
	}

	result := callbackResult{}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.panic = recovered
		}
		done <- result
	}()
	result.err = fn(ctx, grant)
}

func (c *clientCore) renewLoop(parent context.Context, cancelWork context.CancelFunc, stop <-chan struct{}, done chan<- struct{}, lost chan<- error, lease *leaseHandle) {
	defer close(done)
	ticker := time.NewTicker(c.config.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
		select {
		case <-parent.Done():
			return
		case <-stop:
			return
		default:
		}
		if err := lease.Renew(parent); err != nil {
			if parent.Err() != nil {
				return
			}
			cancelWork()
			lost <- errors.Join(ErrLeaseLost, err)
			return
		}
	}
}

func (c *clientCore) releaseRaw(ctx context.Context, lease Lease) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.config.cleanupTimeout)
	defer cancel()
	return lease.Release(cleanupCtx)
}

func newOwner() (string, error) {
	var value [leaseOwnerBytes]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate lease owner: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
