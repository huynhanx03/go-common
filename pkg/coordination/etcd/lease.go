package etcd

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type lease struct {
	adapter *Adapter
	key     string
	owner   string
	id      clientv3.LeaseID
	fence   int64

	mu     sync.Mutex
	closed bool
	lost   error
}

type fencedLease struct {
	*lease
	grant coordination.Grant
}

func (l *fencedLease) Grant() coordination.Grant { return l.grant }

func (l *lease) Renew(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.lost != nil {
		return errors.Join(coordination.ErrLeaseLost, l.lost)
	}
	if ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	response, err := l.adapter.client.Lease.KeepAliveOnce(ctx, l.id)
	if err != nil {
		if errors.Is(err, rpctypes.ErrLeaseNotFound) {
			l.lost = errors.Join(coordination.ErrLeaseLost, err)
			return l.lost
		}
		l.lost = errors.Join(coordination.ErrAmbiguous, fmt.Errorf("renew lease: %w", err))
		return l.lost
	}
	if response == nil || response.TTL <= 0 {
		l.lost = coordination.ErrLeaseLost
		return l.lost
	}
	if err := l.adapter.observeCluster(response.ResponseHeader); err != nil {
		l.lost = err
		return err
	}
	if err := l.verify(ctx); err != nil {
		l.lost = err
		return err
	}
	return nil
}

func (l *lease) verify(ctx context.Context) error {
	response, err := l.adapter.client.Txn(ctx).
		If(leaseComparisons(l.key, l.owner, l.id, l.fence)...).
		Then(clientv3.OpGet(l.key)).
		Commit()
	if err != nil {
		return errors.Join(coordination.ErrAmbiguous, fmt.Errorf("verify lease ownership: %w", err))
	}
	if response == nil || !response.Succeeded {
		return coordination.ErrLeaseLost
	}
	if err := l.adapter.observeCluster(response.Header); err != nil {
		return err
	}
	value := readTxnKey(response)
	if !exactOwner(value, l.owner, l.id) || value.kv.CreateRevision != l.fence {
		return coordination.ErrLeaseLost
	}
	return nil
}

func (l *lease) Release(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	if ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	response, txnErr := l.adapter.client.Txn(ctx).
		If(leaseComparisons(l.key, l.owner, l.id, l.fence)...).
		Then(clientv3.OpDelete(l.key)).
		Else(clientv3.OpGet(l.key)).
		Commit()

	if txnErr == nil && response != nil {
		if err := l.adapter.observeCluster(response.Header); err != nil {
			txnErr = err
		} else if response.Succeeded {
			if err := l.revoke(ctx); err != nil {
				return err
			}
			l.closed = true
			return nil
		} else {
			if err := l.revoke(ctx); err != nil {
				return errors.Join(coordination.ErrLeaseLost, err)
			}
			l.closed = true
			return coordination.ErrLeaseLost
		}
	}

	// Revoking this unique lease is safe even when the delete response is lost.
	if err := l.revoke(ctx); err != nil {
		return errors.Join(coordination.ErrAmbiguous, txnErr, err)
	}
	l.closed = true
	if txnErr != nil {
		return nil
	}
	return errors.Join(coordination.ErrAmbiguous, ErrInvalidConfig)
}

func (l *lease) revoke(ctx context.Context) error {
	_, err := l.adapter.client.Lease.Revoke(ctx, l.id)
	if errors.Is(err, rpctypes.ErrLeaseNotFound) {
		return nil
	}
	if err != nil {
		return errors.Join(coordination.ErrAmbiguous, fmt.Errorf("revoke lease: %w", err))
	}
	return nil
}

func leaseComparisons(key, owner string, id clientv3.LeaseID, revision int64) []clientv3.Cmp {
	return []clientv3.Cmp{
		clientv3.Compare(clientv3.CreateRevision(key), compareEqual, revision),
		clientv3.Compare(clientv3.Value(key), compareEqual, owner),
		clientv3.Compare(clientv3.LeaseValue(key), compareEqual, int64(id)),
	}
}
