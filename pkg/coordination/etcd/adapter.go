package etcd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Adapter implements advisory and fenced leases with a caller-owned etcd client.
type Adapter struct {
	client    *clientv3.Client
	namespace string

	clusterMu sync.Mutex
	clusterID uint64
}

// New creates a namespaced adapter over a caller-owned etcd client.
func New(client *clientv3.Client, namespace string) (*Adapter, error) {
	if client == nil || namespace == "" {
		return nil, ErrInvalidConfig
	}
	return &Adapter{client: client, namespace: namespace}, nil
}

func (a *Adapter) observeCluster(header interface{ GetClusterId() uint64 }) error {
	if header == nil || header.GetClusterId() == 0 {
		return coordination.ErrInvalidFence
	}
	clusterID := header.GetClusterId()
	a.clusterMu.Lock()
	defer a.clusterMu.Unlock()
	if a.clusterID == 0 {
		a.clusterID = clusterID
		return nil
	}
	if a.clusterID != clusterID {
		return errors.Join(coordination.ErrAuthorityMismatch, ErrClusterChanged)
	}
	return nil
}

func (a *Adapter) authority(clusterID uint64) string {
	namespace := base64.RawURLEncoding.EncodeToString([]byte(a.namespace))
	return leaseAuthority + "/" + strconv.FormatUint(clusterID, 16) + "/" + namespace
}

func (a *Adapter) revokeDetached(ctx context.Context, id clientv3.LeaseID) error {
	if id == clientv3.NoLease {
		return nil
	}
	cleanupCtx, cancel := detachedTimeout(ctx, defaultCleanupTimeout)
	defer cancel()
	_, err := a.client.Lease.Revoke(cleanupCtx, id)
	if errors.Is(err, rpctypes.ErrLeaseNotFound) {
		return nil
	}
	if err != nil {
		return errors.Join(coordination.ErrAmbiguous, fmt.Errorf("revoke lease: %w", err))
	}
	return nil
}
