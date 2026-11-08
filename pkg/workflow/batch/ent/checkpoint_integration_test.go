//go:build integration

package ent_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	enttest "github.com/huynhanx03/go-common/pkg/testutil/ent"
	fixture "github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture"
	gen "github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent"
	"github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent/item"
	"github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent/ledger"
	"github.com/huynhanx03/go-common/pkg/workflow/batch"
	"go.uber.org/zap"
)

func TestBatchCheckpointIntegration(t *testing.T) {
	_, driver := enttest.OpenPostgres(t)
	client := gen.NewClient(gen.Driver(driver))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := fixture.Migrate(ctx, client); err != nil {
		t.Fatal(err)
	}
	store := fixture.NewStore(client, "authority-a")
	log := logger.Wrap(zap.NewNop())

	t.Run("page transaction rolls back business effects and checkpoint together", func(t *testing.T) {
		definition := integrationDefinition("rollback")
		runner := integrationRunner(t, store, log)
		failure := errors.New("business write rejected")
		result, err := runner.Run(ctx, definition, func(stepCtx context.Context, current batch.Checkpoint) (batch.Progress, error) {
			if err := writeEffects(stepCtx, definition.Key, current.Version); err != nil {
				return batch.Progress{}, err
			}
			return batch.Progress{}, failure
		})
		if !errors.Is(err, failure) || result.Version != 0 {
			t.Fatalf("failed page result = %#v, %v", result, err)
		}
		assertEffects(t, ctx, client, definition.Key, 0)
		checkpoint, err := store.Get(ctx, definition.Key)
		if err != nil || checkpoint.Version != 0 || checkpoint.ProcessedCount != 0 || checkpoint.Status != batch.Ready {
			t.Fatalf("rollback checkpoint = %#v, %v", checkpoint, err)
		}
	})

	t.Run("committed response loss is reconciled once and completed identity stays immutable", func(t *testing.T) {
		definition := integrationDefinition("response-loss")
		wrapped := &responseLossStore{Store: store}
		runner := integrationRunner(t, wrapped, log)
		var calls atomic.Int64
		step := func(stepCtx context.Context, current batch.Checkpoint) (batch.Progress, error) {
			calls.Add(1)
			if err := writeEffects(stepCtx, definition.Key, current.Version); err != nil {
				return batch.Progress{}, err
			}
			return batch.Progress{Cursor: []byte{byte(current.Version + 1)}, Processed: 1, Done: current.Version == 1}, nil
		}
		result, err := runner.Run(ctx, definition, step)
		if err != nil || !result.Done || result.Version != 2 || result.ProcessedCount != 2 {
			t.Fatalf("recovered run = %#v, %v", result, err)
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("business step ran %d times; want one execution per committed page", got)
		}
		assertEffects(t, ctx, client, definition.Key, 2)
		if repeated, err := runner.Run(ctx, definition, step); err != nil || repeated != result || calls.Load() != 2 {
			t.Fatalf("completed rerun = %#v, %v, calls %d", repeated, err, calls.Load())
		}
		mismatch := definition
		mismatch.InputFingerprint = "different-input"
		if _, err := runner.Run(ctx, mismatch, step); !errors.Is(err, batch.ErrDefinitionConflict) {
			t.Fatalf("completed identity mismatch = %v; want definition conflict", err)
		}
		mismatch = definition
		mismatch.Boundary = []byte("different-boundary")
		if _, err := runner.Run(ctx, mismatch, step); !errors.Is(err, batch.ErrDefinitionConflict) {
			t.Fatalf("completed boundary mismatch = %v; want definition conflict", err)
		}
	})

	t.Run("takeover advances fence and old owner cannot renew, release, or apply", func(t *testing.T) {
		definition := integrationDefinition("takeover")
		if _, err := store.Open(ctx, definition); err != nil {
			t.Fatal(err)
		}
		first, err := store.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: definition.Key, Owner: "owner-a", TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		firstGrant := first.Grant()
		if err := first.Release(ctx); err != nil {
			t.Fatal(err)
		}
		second, err := store.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: definition.Key, Owner: "owner-b", TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		secondGrant := second.Grant()
		if secondGrant.Fence.Value <= firstGrant.Fence.Value {
			t.Fatalf("new fence %d did not advance beyond %d", secondGrant.Fence.Value, firstGrant.Fence.Value)
		}
		if err := first.Release(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
			t.Fatalf("stale release = %v; want lease lost", err)
		}
		if _, err := client.ExecContext(ctx, "UPDATE checkpoints SET lease_until = clock_timestamp() - INTERVAL '1 second' WHERE job_key = $1", definition.Key); err != nil {
			t.Fatal(err)
		}
		if err := second.Renew(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
			t.Fatalf("expired renewal = %v; want lease lost", err)
		}
		third, err := store.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: definition.Key, Owner: "owner-c", TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if third.Grant().Fence.Value <= secondGrant.Fence.Value {
			t.Fatalf("takeover fence %d did not advance beyond %d", third.Grant().Fence.Value, secondGrant.Fence.Value)
		}
		var staleCalls atomic.Int64
		_, err = store.Apply(ctx, batch.StepRequest{Grant: secondGrant, ExpectedVersion: 0, StepID: "old-step"}, func(context.Context, batch.Checkpoint) (batch.Progress, error) {
			staleCalls.Add(1)
			return batch.Progress{Cursor: []byte{1}, Processed: 1}, nil
		})
		if !errors.Is(err, coordination.ErrLeaseLost) || staleCalls.Load() != 0 {
			t.Fatalf("stale apply = %v after %d callbacks", err, staleCalls.Load())
		}
		if err := third.Release(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("row lock orders an in-flight page before a waiting takeover", func(t *testing.T) {
		definition := integrationDefinition("row-lock")
		if _, err := store.Open(ctx, definition); err != nil {
			t.Fatal(err)
		}
		first, err := store.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: definition.Key, Owner: "writer-a", TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		applyStarted := make(chan struct{})
		allowCommit := make(chan struct{})
		applyDone := make(chan error, 1)
		go func() {
			_, err := store.Apply(ctx, batch.StepRequest{Grant: first.Grant(), ExpectedVersion: 0, StepID: "writer-a/0"}, func(stepCtx context.Context, _ batch.Checkpoint) (batch.Progress, error) {
				tx := gen.TxFromContext(stepCtx)
				if _, err := tx.Client().ExecContext(stepCtx, "UPDATE checkpoints SET lease_until = clock_timestamp() - INTERVAL '1 second' WHERE job_key = $1", definition.Key); err != nil {
					return batch.Progress{}, err
				}
				close(applyStarted)
				<-allowCommit
				return batch.Progress{Cursor: []byte{1}, Processed: 1}, nil
			})
			applyDone <- err
		}()
		<-applyStarted
		claimStarted := make(chan struct{})
		type claimResult struct {
			lease coordination.FencedLease
			err   error
		}
		claimDone := make(chan claimResult, 1)
		go func() {
			close(claimStarted)
			lease, err := store.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: definition.Key, Owner: "writer-b", TTL: time.Minute})
			claimDone <- claimResult{lease: lease, err: err}
		}()
		<-claimStarted
		assertLockWait(t, ctx, client)
		close(allowCommit)
		if err := <-applyDone; err != nil {
			t.Fatalf("in-flight page failed: %v", err)
		}
		claim := <-claimDone
		if claim.err != nil {
			t.Fatal(claim.err)
		}
		if claim.lease.Grant().Fence.Value <= first.Grant().Fence.Value {
			t.Fatalf("takeover fence %d did not advance", claim.lease.Grant().Fence.Value)
		}
		checkpoint, err := store.Get(ctx, definition.Key)
		if err != nil || checkpoint.Version != 1 || checkpoint.Fence.Value != claim.lease.Grant().Fence.Value {
			t.Fatalf("ordered checkpoint = %#v, %v", checkpoint, err)
		}
		if err := first.Renew(ctx); !errors.Is(err, coordination.ErrLeaseLost) {
			t.Fatalf("old writer renewed after takeover: %v", err)
		}
		if err := claim.lease.Release(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("completion between open and claim is returned as completed", func(t *testing.T) {
		definition := integrationDefinition("completion-race")
		wrapped := &completeAfterOpenStore{Store: store}
		runner := integrationRunner(t, wrapped, log)
		result, err := runner.Run(ctx, definition, func(context.Context, batch.Checkpoint) (batch.Progress, error) {
			t.Fatal("step must not run after another worker completed the job")
			return batch.Progress{}, nil
		})
		if err != nil || !result.Done || result.Version != 1 {
			t.Fatalf("completed race result = %#v, %v", result, err)
		}
	})

	t.Run("stored authority cannot be silently replaced", func(t *testing.T) {
		definition := integrationDefinition("authority")
		if _, err := store.Open(ctx, definition); err != nil {
			t.Fatal(err)
		}
		other := fixture.NewStore(client, "authority-b")
		if _, err := other.Open(ctx, definition); !errors.Is(err, coordination.ErrAuthorityMismatch) {
			t.Fatalf("replacement authority = %v; want authority mismatch", err)
		}
	})

	t.Run("database constraints protect progress and fence high-water marks", func(t *testing.T) {
		definition := integrationDefinition("constraints")
		if _, err := store.Open(ctx, definition); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ExecContext(ctx, "UPDATE checkpoints SET version = -1, processed_count = -1, fence = -1 WHERE job_key = $1", definition.Key); err == nil {
			t.Fatal("database accepted negative checkpoint progress and fence values")
		}
		checkpoint, err := store.Get(ctx, definition.Key)
		if err != nil || checkpoint.Version != 0 || checkpoint.ProcessedCount != 0 || checkpoint.Fence.Value != 0 {
			t.Fatalf("rejected constraint update changed checkpoint: %#v, %v", checkpoint, err)
		}
	})
}

func assertLockWait(t *testing.T, ctx context.Context, client *gen.Client) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, err := client.QueryContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND state = 'active'")
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if !rows.Next() {
			err := rows.Err()
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Scan(&count); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
		if count > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("takeover did not wait for the checkpoint row lock")
		case <-ticker.C:
		}
	}
}

func writeEffects(ctx context.Context, key string, page int64) error {
	tx := gen.TxFromContext(ctx)
	if tx == nil {
		return errors.New("batch callback did not receive the Ent transaction")
	}
	pageKey := fmt.Sprintf("page-%d", page)
	if _, err := tx.Item.Create().SetJobKey(key).SetItemKey(pageKey).Save(ctx); err != nil {
		return err
	}
	_, err := tx.Ledger.Create().SetJobKey(key).SetProcessed(1).Save(ctx)
	return err
}

func assertEffects(t *testing.T, ctx context.Context, client *gen.Client, key string, expected int) {
	t.Helper()
	items, err := client.Item.Query().Where(item.JobKeyEQ(key)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := client.Ledger.Query().Where(ledger.JobKeyEQ(key)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items != expected || entries != expected {
		t.Fatalf("committed effects = %d items, %d ledger entries; want %d each", items, entries, expected)
	}
}

func integrationDefinition(key string) batch.Definition {
	return batch.Definition{Key: "integration/" + key, InputFingerprint: "source-v1", CursorSchema: "cursor-v1", Boundary: []byte("cutoff")}
}

func integrationRunner(t *testing.T, store batch.Store, log *logger.Logger) *batch.Runner {
	t.Helper()
	runner, err := batch.New(store, log, batch.Config{
		Lease:       settings.Coordination{TTLMilliseconds: 3000, RenewIntervalMilliseconds: 1000, OperationTimeoutMilliseconds: 300, CleanupTimeoutMilliseconds: 1000},
		PageTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type responseLossStore struct {
	*fixture.Store
	lost atomic.Bool
}

func (s *responseLossStore) Apply(ctx context.Context, request batch.StepRequest, step batch.Step) (batch.Checkpoint, error) {
	checkpoint, err := s.Store.Apply(ctx, request, step)
	if err == nil && s.lost.CompareAndSwap(false, true) {
		return checkpoint, coordination.ErrAmbiguous
	}
	return checkpoint, err
}

type completeAfterOpenStore struct{ *fixture.Store }

func (s *completeAfterOpenStore) Open(ctx context.Context, definition batch.Definition) (batch.Checkpoint, error) {
	stale, err := s.Store.Open(ctx, definition)
	if err != nil {
		return batch.Checkpoint{}, err
	}
	lease, err := s.Store.TryAcquireFenced(ctx, coordination.AcquireRequest{Resource: definition.Key, Owner: "race-completer", TTL: time.Minute})
	if err != nil {
		return batch.Checkpoint{}, err
	}
	_, err = s.Store.Apply(ctx, batch.StepRequest{Grant: lease.Grant(), ExpectedVersion: 0, StepID: "race-completer/0"}, func(context.Context, batch.Checkpoint) (batch.Progress, error) {
		return batch.Progress{Cursor: []byte{1}, Processed: 1, Done: true}, nil
	})
	if err != nil {
		_ = lease.Release(ctx)
		return batch.Checkpoint{}, err
	}
	if err := lease.Release(ctx); err != nil {
		return batch.Checkpoint{}, err
	}
	return stale, nil
}
