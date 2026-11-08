package etcd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func (a *Adapter) TryAcquire(ctx context.Context, request coordination.AcquireRequest) (coordination.Lease, error) {
	lease, _, err := a.acquire(ctx, request)
	if err != nil {
		return nil, err
	}
	return lease, nil
}

func (a *Adapter) TryAcquireFenced(ctx context.Context, request coordination.AcquireRequest) (coordination.FencedLease, error) {
	lease, grant, err := a.acquire(ctx, request)
	if err != nil {
		return nil, err
	}
	return &fencedLease{lease: lease, grant: grant}, nil
}

func (a *Adapter) acquire(ctx context.Context, request coordination.AcquireRequest) (*lease, coordination.Grant, error) {
	if err := validateAcquire(ctx, request); err != nil {
		return nil, coordination.Grant{}, err
	}
	ttl, err := ttlSeconds(request.TTL)
	if err != nil {
		return nil, coordination.Grant{}, err
	}
	key := ownershipKey(a.namespace, request.Resource)
	grantResponse, err := a.client.Lease.Grant(ctx, ttl)
	if err != nil {
		return nil, coordination.Grant{}, errors.Join(coordination.ErrAmbiguous, fmt.Errorf("grant lease: %w", err))
	}
	if grantResponse == nil || grantResponse.ID == clientv3.NoLease || grantResponse.TTL <= 0 {
		if grantResponse != nil && grantResponse.ID != clientv3.NoLease {
			return nil, coordination.Grant{}, errors.Join(coordination.ErrAmbiguous, ErrInvalidConfig, a.revokeDetached(ctx, grantResponse.ID))
		}
		return nil, coordination.Grant{}, errors.Join(coordination.ErrAmbiguous, ErrInvalidConfig)
	}
	if err := a.observeCluster(grantResponse.ResponseHeader); err != nil {
		return nil, coordination.Grant{}, errors.Join(err, a.revokeDetached(ctx, grantResponse.ID))
	}

	txnResponse, txnErr := a.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(key), compareEqual, 0)).
		Then(clientv3.OpPut(key, request.Owner, clientv3.WithLease(grantResponse.ID))).
		Else(clientv3.OpGet(key)).
		Commit()

	if txnErr == nil && txnResponse != nil && txnResponse.Succeeded {
		if err := a.observeCluster(txnResponse.Header); err != nil {
			return nil, coordination.Grant{}, errors.Join(err, a.revokeDetached(ctx, grantResponse.ID))
		}
		if txnResponse.Header == nil || txnResponse.Header.Revision <= 0 {
			return nil, coordination.Grant{}, errors.Join(coordination.ErrAmbiguous, a.revokeDetached(ctx, grantResponse.ID))
		}
		return a.newLease(request, key, grantResponse.ID, txnResponse.Header.Revision, txnResponse.Header.ClusterId)
	}

	if txnErr == nil && txnResponse != nil && !txnResponse.Succeeded {
		if existing := readTxnKey(txnResponse); exactOwner(existing, request.Owner, grantResponse.ID) {
			if err := a.observeCluster(txnResponse.Header); err != nil {
				return nil, coordination.Grant{}, errors.Join(err, a.revokeDetached(ctx, grantResponse.ID))
			}
			return a.newLease(request, key, grantResponse.ID, existing.kv.CreateRevision, txnResponse.Header.ClusterId)
		}
		if err := a.revokeDetached(ctx, grantResponse.ID); err != nil {
			return nil, coordination.Grant{}, errors.Join(coordination.ErrHeld, err)
		}
		return nil, coordination.Grant{}, coordination.ErrHeld
	}

	if txnErr != nil {
		// A lost transaction response is reconciled by the exact attempt owner and lease.
		verifyCtx, cancel := detachedTimeout(ctx, defaultCleanupTimeout)
		owner, verifyErr := a.readOwner(verifyCtx, key)
		cancel()
		if verifyErr == nil && exactOwner(owner, request.Owner, grantResponse.ID) {
			if err := a.observeCluster(owner.header); err == nil {
				return a.newLease(request, key, grantResponse.ID, owner.kv.CreateRevision, owner.header.GetClusterId())
			} else {
				verifyErr = err
			}
		}
		cleanupErr := a.revokeDetached(ctx, grantResponse.ID)
		return nil, coordination.Grant{}, errors.Join(coordination.ErrAmbiguous, fmt.Errorf("acquire lease: %w", txnErr), verifyErr, cleanupErr)
	}

	return nil, coordination.Grant{}, errors.Join(coordination.ErrAmbiguous, ErrInvalidConfig, a.revokeDetached(ctx, grantResponse.ID))
}

func (a *Adapter) newLease(request coordination.AcquireRequest, key string, id clientv3.LeaseID, revision int64, clusterID uint64) (*lease, coordination.Grant, error) {
	if revision <= 0 || clusterID == 0 {
		return nil, coordination.Grant{}, coordination.ErrInvalidFence
	}
	grant := coordination.Grant{
		Resource: request.Resource,
		Owner:    request.Owner,
		Fence: coordination.FenceToken{
			Authority: a.authority(clusterID),
			Value:     revision,
		},
	}
	return &lease{adapter: a, key: key, owner: request.Owner, id: id, fence: revision}, grant, nil
}

func validateAcquire(ctx context.Context, request coordination.AcquireRequest) error {
	if ctx == nil || request.Resource == "" || request.Owner == "" || request.TTL <= 0 {
		return ErrInvalidConfig
	}
	return ctx.Err()
}

func ttlSeconds(ttl time.Duration) (int64, error) {
	seconds := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		seconds++
	}
	if seconds <= 0 {
		return 0, ErrInvalidConfig
	}
	return seconds, nil
}

func ownershipKey(namespace, resource string) string {
	encodedNamespace := base64.RawURLEncoding.EncodeToString([]byte(namespace))
	encodedResource := base64.RawURLEncoding.EncodeToString([]byte(resource))
	return ownershipKeyPrefix + "/" + encodedNamespace + "/" + encodedResource
}

type ownerValue struct {
	kv     *mvccpb.KeyValue
	header interface{ GetClusterId() uint64 }
}

func (a *Adapter) readOwner(ctx context.Context, key string) (ownerValue, error) {
	response, err := a.client.Get(ctx, key)
	if err != nil {
		return ownerValue{}, err
	}
	if response == nil || response.Header == nil {
		return ownerValue{}, coordination.ErrAmbiguous
	}
	if len(response.Kvs) != 1 {
		return ownerValue{header: response.Header}, nil
	}
	return ownerValue{kv: response.Kvs[0], header: response.Header}, nil
}

func exactOwner(value ownerValue, owner string, id clientv3.LeaseID) bool {
	return value.kv != nil && string(value.kv.Value) == owner && value.kv.Lease == int64(id) && value.kv.CreateRevision > 0
}

func readTxnKey(response *clientv3.TxnResponse) ownerValue {
	if response == nil || response.Header == nil || len(response.Responses) != 1 {
		return ownerValue{}
	}
	rangeResponse := response.Responses[0].GetResponseRange()
	if rangeResponse == nil || len(rangeResponse.Kvs) != 1 {
		return ownerValue{header: response.Header}
	}
	return ownerValue{kv: rangeResponse.Kvs[0], header: response.Header}
}

func detachedTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}
