//go:build integration

package etcd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	_ "github.com/lib/pq"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEtcdLeaseIntegration(t *testing.T) {
	t.Run("fences follow ownership creation and stale handles cannot affect a new owner", func(t *testing.T) {
		cluster := containers.StartEtcd(t, 1)
		client := newClient(t, cluster.Endpoints(), nil)
		adapter := newAdapter(t, client, "tenant/a")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		request := coordination.AcquireRequest{Resource: "job/one", Owner: "owner-a", TTL: 6 * time.Second}
		first, err := adapter.TryAcquireFenced(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		firstFence := first.Grant().Fence
		if firstFence.Value <= 0 || firstFence.Authority == "" {
			t.Fatalf("fence = %#v; want a positive revision and authority", firstFence)
		}
		if err := first.Renew(ctx); err != nil {
			t.Fatalf("renew first lease: %v", err)
		}
		if got := first.Grant().Fence; got != firstFence {
			t.Fatalf("renew changed fence: got %#v, want %#v", got, firstFence)
		}

		// Revoke the first lease externally to model expiry while keeping its local
		// handle alive; the old handle must not delete or revoke the new owner.
		if _, err := client.Lease.Revoke(ctx, first.(*fencedLease).lease.id); err != nil {
			t.Fatalf("expire first lease: %v", err)
		}
		if err := first.Renew(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
			t.Fatalf("renew expired lease = %v; want lease lost", err)
		}
		second, err := adapter.TryAcquireFenced(ctx, coordination.AcquireRequest{
			Resource: request.Resource,
			Owner:    "owner-b",
			TTL:      request.TTL,
		})
		if err != nil {
			t.Fatalf("acquire after expiry: %v", err)
		}
		secondFence := second.Grant().Fence
		if secondFence.Value <= firstFence.Value {
			t.Fatalf("new fence %d did not increase after %d", secondFence.Value, firstFence.Value)
		}
		if err := first.Release(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
			t.Fatalf("stale release = %v; want lease lost", err)
		}
		if err := second.Renew(ctx); err != nil {
			t.Fatalf("stale owner affected new lease: %v", err)
		}
		if err := second.Release(ctx); err != nil {
			t.Fatalf("release second lease: %v", err)
		}
		if err := second.Release(ctx); err != nil {
			t.Fatalf("repeat release = %v; want idempotent success", err)
		}
		if err := second.Renew(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
			t.Fatalf("renew released lease = %v; want lease lost", err)
		}
	})

	t.Run("concurrent owners contend and namespaces isolate resources", func(t *testing.T) {
		cluster := containers.StartEtcd(t, 1)
		client := newClient(t, cluster.Endpoints(), nil)
		firstNamespace := newAdapter(t, client, "tenant/a")
		secondNamespace := newAdapter(t, client, "tenant/b")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		leaseA, err := firstNamespace.TryAcquireFenced(ctx, coordination.AcquireRequest{
			Resource: "shared", Owner: "namespace-a", TTL: 6 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		leaseB, err := secondNamespace.TryAcquireFenced(ctx, coordination.AcquireRequest{
			Resource: "shared", Owner: "namespace-b", TTL: 6 * time.Second,
		})
		if err != nil {
			t.Fatalf("same resource in a separate namespace contended: %v", err)
		}
		if leaseA.Grant().Fence.Authority == leaseB.Grant().Fence.Authority {
			t.Fatal("separate namespaces received the same fence authority")
		}
		if err := leaseA.Release(ctx); err != nil {
			t.Fatal(err)
		}
		if err := leaseB.Release(ctx); err != nil {
			t.Fatal(err)
		}

		const contenders = 12
		var successes atomic.Int32
		var winner coordination.FencedLease
		var winnerMu sync.Mutex
		var wait sync.WaitGroup
		for i := range contenders {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				lease, err := firstNamespace.TryAcquireFenced(ctx, coordination.AcquireRequest{
					Resource: "one-winner", Owner: fmt.Sprintf("owner-%d", i), TTL: 6 * time.Second,
				})
				if errors.Is(err, coordination.ErrHeld) {
					return
				}
				if err != nil {
					t.Errorf("contender %d: %v", i, err)
					return
				}
				successes.Add(1)
				winnerMu.Lock()
				winner = lease
				winnerMu.Unlock()
			}(i)
		}
		wait.Wait()
		if got := successes.Load(); got != 1 {
			t.Fatalf("successful owners = %d; want exactly one", got)
		}
		if err := winner.Release(ctx); err != nil {
			t.Fatalf("release winning lease: %v", err)
		}
	})
}

func TestEtcdAdvisoryLeaseIntegration(t *testing.T) {
	cluster := containers.StartEtcd(t, 1)
	client := newClient(t, cluster.Endpoints(), nil)
	adapter := newAdapter(t, client, "advisory")
	leases, err := coordination.New(adapter, logger.Wrap(zap.NewNop()), integrationConfig())
	if err != nil {
		t.Fatalf("create advisory lease client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := leases.TryAcquire(ctx, "worker/one")
	if err != nil {
		t.Fatalf("first TryAcquire: %v", err)
	}
	if _, err := leases.TryAcquire(ctx, "worker/one"); !errors.Is(err, coordination.ErrHeld) {
		t.Fatalf("contending TryAcquire error = %v; want lease held", err)
	}
	if err := first.Release(ctx); err != nil {
		t.Fatalf("release first lease: %v", err)
	}
	second, err := leases.TryAcquire(ctx, "worker/one")
	if err != nil {
		t.Fatalf("TryAcquire after release: %v", err)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatalf("release second lease: %v", err)
	}

	canceled, cancelCall := context.WithCancel(ctx)
	cancelCall()
	if _, err := leases.TryAcquire(canceled, "worker/one"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled TryAcquire error = %v; want context canceled", err)
	}
}

func TestEtcdReleaseLostResponseIntegration(t *testing.T) {
	cluster := containers.StartEtcd(t, 1)
	var loseResponse atomic.Bool
	var injected atomic.Bool
	client := newClient(t, cluster.Endpoints(), grpc.WithUnaryInterceptor(func(
		ctx context.Context,
		method string,
		request, response any,
		connection *grpc.ClientConn,
		invoke grpc.UnaryInvoker,
		options ...grpc.CallOption,
	) error {
		err := invoke(ctx, method, request, response, connection, options...)
		if err != nil || method != "/etcdserverpb.KV/Txn" || !loseResponse.CompareAndSwap(true, false) {
			return err
		}
		if txn, ok := response.(*etcdserverpb.TxnResponse); ok && txn.Succeeded {
			injected.Store(true)
			return status.Error(codes.Unavailable, "injected release response loss")
		}
		return err
	}))
	adapter := newAdapter(t, client, "release-response-loss")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := adapter.TryAcquireFenced(ctx, coordination.AcquireRequest{
		Resource: "worker/one", Owner: "first", TTL: 6 * time.Second,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	loseResponse.Store(true)
	if err := first.Release(ctx); err != nil {
		t.Fatalf("release after committed delete response was lost: %v", err)
	}
	if !injected.Load() {
		t.Fatal("release transaction response was not lost after commit")
	}
	second, err := adapter.TryAcquireFenced(ctx, coordination.AcquireRequest{
		Resource: "worker/one", Owner: "second", TTL: 6 * time.Second,
	})
	if err != nil {
		t.Fatalf("acquire after ambiguous release: %v", err)
	}
	if second.Grant().Fence.Value <= first.Grant().Fence.Value {
		t.Fatalf("next fence %d did not advance beyond %d", second.Grant().Fence.Value, first.Grant().Fence.Value)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatalf("release second lease: %v", err)
	}
}

func TestEtcdLeaseFailureIntegration(t *testing.T) {
	t.Run("leader failover preserves ordered grants and quorum loss never runs work", func(t *testing.T) {
		cluster := containers.StartEtcd(t, 3)
		client := newClient(t, cluster.Endpoints(), nil)
		adapter := newAdapter(t, client, "failover")
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()

		first, err := adapter.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: "leader", Owner: "before", TTL: 15 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		leaderIndex, oldLeader := waitForLeader(t, ctx, client, cluster.Endpoints(), 0)
		if err := cluster.StopMember(ctx, leaderIndex); err != nil {
			t.Fatalf("stop leader %d: %v", leaderIndex, err)
		}
		newLeader, _ := waitForLeader(t, ctx, client, cluster.Endpoints(), oldLeader)
		if err := first.Release(ctx); err != nil {
			t.Fatalf("release pre-failover lease: %v", err)
		}
		second, err := adapter.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: "leader", Owner: "after", TTL: 15 * time.Second})
		if err != nil {
			t.Fatalf("acquire after leader failover: %v", err)
		}
		if second.Grant().Fence.Value <= first.Grant().Fence.Value {
			t.Fatalf("post-failover fence %d did not exceed %d", second.Grant().Fence.Value, first.Grant().Fence.Value)
		}

		for index := range cluster.Endpoints() {
			if index == newLeader || index == leaderIndex {
				continue
			}
			if err := cluster.StopMember(ctx, index); err != nil {
				t.Fatalf("stop member %d to remove quorum: %v", index, err)
			}
			break
		}
		renewCtx, renewCancel := context.WithTimeout(ctx, 2*time.Second)
		renewErr := second.Renew(renewCtx)
		renewCancel()
		if !errors.Is(renewErr, coordination.ErrAmbiguous) {
			t.Fatalf("renew without quorum = %v; want ambiguous lease loss", renewErr)
		}
		fencedClient, err := coordination.NewFenced(adapter, logger.Wrap(zap.NewNop()), integrationConfig())
		if err != nil {
			t.Fatal(err)
		}
		noQuorumCtx, noQuorumCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer noQuorumCancel()
		var callbacks atomic.Int32
		err = fencedClient.Do(noQuorumCtx, "no-quorum", func(context.Context, coordination.Grant) error {
			callbacks.Add(1)
			return nil
		})
		if err == nil || callbacks.Load() != 0 {
			t.Fatalf("Do() = %v with %d callbacks; quorum loss must fail closed", err, callbacks.Load())
		}
	})

	t.Run("a lost acquire response is reconciled only for the exact attempt", func(t *testing.T) {
		for _, removeKey := range []bool{false, true} {
			name := "exact ownership resumes callback"
			if removeKey {
				name = "missing ownership rejects callback"
			}
			t.Run(name, func(t *testing.T) {
				cluster := containers.StartEtcd(t, 1)
				control := newClient(t, cluster.Endpoints(), nil)
				const namespace = "response-loss"
				const resource = "job"
				var injected atomic.Bool
				client := newClient(t, cluster.Endpoints(), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, request, response any, connection *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
					err := invoke(ctx, method, request, response, connection, options...)
					if err == nil && method == "/etcdserverpb.KV/Txn" && injected.CompareAndSwap(false, true) {
						if txn, ok := response.(*etcdserverpb.TxnResponse); ok && txn.Succeeded {
							if removeKey {
								_, _ = control.Delete(context.Background(), ownershipKey(namespace, resource))
							}
							return status.Error(codes.Unknown, "injected response loss after commit")
						}
					}
					return err
				}))
				adapter := newAdapter(t, client, namespace)
				fencedClient, err := coordination.NewFenced(adapter, logger.Wrap(zap.NewNop()), integrationConfig())
				if err != nil {
					t.Fatal(err)
				}
				var callbacks atomic.Int32
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				err = fencedClient.Do(ctx, resource, func(context.Context, coordination.Grant) error {
					callbacks.Add(1)
					return nil
				})
				if removeKey {
					if !errors.Is(err, coordination.ErrAmbiguous) || callbacks.Load() != 0 {
						t.Fatalf("Do() = %v with %d callbacks; want ambiguous and no callback", err, callbacks.Load())
					}
				} else if err != nil || callbacks.Load() != 1 {
					t.Fatalf("Do() = %v with %d callbacks; want reconciled callback once", err, callbacks.Load())
				}
			})
		}
	})
}

func TestFencedResourceIntegration(t *testing.T) {
	cluster := containers.StartEtcd(t, 1)
	etcdClient := newClient(t, cluster.Endpoints(), nil)
	adapter := newAdapter(t, etcdClient, "database-guard")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	postgres, err := sql.Open("postgres", containers.StartPostgres(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Close() })
	if err := postgres.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = postgres.ExecContext(ctx, `
		CREATE TABLE resource_guard (
			resource_id text PRIMARY KEY,
			authority text NOT NULL,
			high_water bigint NOT NULL CHECK (high_water >= 0)
		);
		CREATE TABLE guarded_ledger (
			operation_id text PRIMARY KEY,
			fence bigint NOT NULL
		);`)
	if err != nil {
		t.Fatal(err)
	}

	first, err := adapter.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: "account/1", Owner: "owner-a", TTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	firstGrant := first.Grant()
	if _, err := postgres.ExecContext(ctx, "INSERT INTO resource_guard(resource_id, authority, high_water) VALUES ($1, $2, $3)", firstGrant.Resource, firstGrant.Fence.Authority, 0); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := adapter.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: firstGrant.Resource, Owner: "owner-b", TTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	secondGrant := second.Grant()
	if secondGrant.Fence.Value <= firstGrant.Fence.Value {
		t.Fatalf("second fence %d did not exceed first fence %d", secondGrant.Fence.Value, firstGrant.Fence.Value)
	}

	for _, operationID := range []string{"b-1", "b-2"} {
		inserted, err := applyGuardedOperation(ctx, postgres, secondGrant, operationID)
		if err != nil || !inserted {
			t.Fatalf("apply %s: inserted=%v err=%v", operationID, inserted, err)
		}
	}
	if inserted, err := applyGuardedOperation(ctx, postgres, secondGrant, "b-1"); err != nil || inserted {
		t.Fatalf("repeat operation: inserted=%v err=%v; want deduplicated", inserted, err)
	}
	if _, err := applyGuardedOperation(ctx, postgres, firstGrant, "a-stale"); !errors.Is(err, coordination.ErrStaleFence) {
		t.Fatalf("stale writer error = %v; want stale fence", err)
	}
	foreignAuthority := secondGrant
	foreignAuthority.Fence.Authority = "etcd/another-cluster/YWNjb3VudA"
	foreignAuthority.Fence.Value += 100
	if _, err := applyGuardedOperation(ctx, postgres, foreignAuthority, "foreign"); !errors.Is(err, coordination.ErrAuthorityMismatch) {
		t.Fatalf("different authority error = %v; want authority mismatch", err)
	}
	var ledgerRows int
	if err := postgres.QueryRowContext(ctx, "SELECT count(*) FROM guarded_ledger").Scan(&ledgerRows); err != nil {
		t.Fatal(err)
	}
	if ledgerRows != 2 {
		t.Fatalf("ledger rows = %d; stale, foreign and duplicate operations must have no effect", ledgerRows)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func applyGuardedOperation(ctx context.Context, database *sql.DB, grant coordination.Grant, operationID string) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var authority string
	var highWater int64
	if err := tx.QueryRowContext(ctx, "SELECT authority, high_water FROM resource_guard WHERE resource_id = $1 FOR UPDATE", grant.Resource).Scan(&authority, &highWater); err != nil {
		return false, err
	}
	if authority != grant.Fence.Authority {
		return false, coordination.ErrAuthorityMismatch
	}
	if grant.Fence.Value < highWater {
		return false, coordination.ErrStaleFence
	}
	if _, err := tx.ExecContext(ctx, "UPDATE resource_guard SET high_water = GREATEST(high_water, $2) WHERE resource_id = $1", grant.Resource, grant.Fence.Value); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO guarded_ledger(operation_id, fence) VALUES ($1, $2) ON CONFLICT (operation_id) DO NOTHING", operationID, grant.Fence.Value)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return inserted == 1, nil
}

func newClient(t *testing.T, endpoints []string, option grpc.DialOption) *clientv3.Client {
	t.Helper()
	options := make([]grpc.DialOption, 0, 1)
	if option != nil {
		options = append(options, option)
	}
	client, err := clientv3.New(clientv3.Config{Endpoints: endpoints, DialTimeout: 10 * time.Second, DialOptions: options})
	if err != nil {
		t.Fatalf("create etcd client for %v: %v", endpoints, err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close etcd client: %v", err)
		}
	})
	return client
}

func newAdapter(t *testing.T, client *clientv3.Client, namespace string) *Adapter {
	t.Helper()
	adapter, err := New(client, namespace)
	if err != nil {
		t.Fatalf("create etcd adapter: %v", err)
	}
	return adapter
}

func integrationConfig() settings.Coordination {
	return settings.Coordination{
		TTLMilliseconds:              15_000,
		RenewIntervalMilliseconds:    5_000,
		OperationTimeoutMilliseconds: 1_000,
		CleanupTimeoutMilliseconds:   1_000,
	}
}

func waitForLeader(t *testing.T, ctx context.Context, client *clientv3.Client, endpoints []string, differentFrom uint64) (int, uint64) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		for index, endpoint := range endpoints {
			statusResponse, err := statusWithTimeout(ctx, client, endpoint)
			if err == nil && statusResponse != nil && statusResponse.Leader != 0 && statusResponse.Leader != differentFrom {
				return indexOfMember(endpoints, client, ctx, statusResponse.Leader, index), statusResponse.Leader
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for etcd leader: %v", ctx.Err())
			return -1, 0
		case <-ticker.C:
		}
	}
}

func indexOfMember(endpoints []string, client *clientv3.Client, ctx context.Context, memberID uint64, fallback int) int {
	for index, endpoint := range endpoints {
		statusResponse, err := statusWithTimeout(ctx, client, endpoint)
		if err == nil && statusResponse != nil && statusResponse.Header != nil && statusResponse.Header.MemberId == memberID {
			return index
		}
	}
	return fallback
}

func statusWithTimeout(ctx context.Context, client *clientv3.Client, endpoint string) (*clientv3.StatusResponse, error) {
	statusCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return client.Status(statusCtx, endpoint)
}
