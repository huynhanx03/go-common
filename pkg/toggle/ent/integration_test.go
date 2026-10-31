//go:build integration

package ent_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/huynhanx03/go-common/pkg/toggle"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
)

func TestPostgresManagerLifecycleAndScopeIsolation(t *testing.T) {
	db, driver, _, manager := newPostgres(t)
	ctx := context.Background()

	empty, err := manager.List(ctx, postgresScope)
	if err != nil || empty.Revision != 0 || len(empty.Flags) != 0 {
		t.Fatalf("initial snapshot = %+v, %v", empty, err)
	}
	revision, err := manager.Put(ctx, postgresScope, integrationFlag("checkout", true), 0)
	if err != nil || revision != 1 {
		t.Fatalf("first Put revision=%d err=%v", revision, err)
	}
	createdAt := readCreatedAt(t, db, "billing", "test", "checkout")
	revision, err = manager.Put(ctx, postgresScope, toggle.Flag{Key: "checkout", Description: "updated", Enabled: false}, 1)
	if err != nil || revision != 2 {
		t.Fatalf("update revision=%d err=%v", revision, err)
	}
	if updated := readCreatedAt(t, db, "billing", "test", "checkout"); !updated.Equal(createdAt) {
		t.Fatalf("updated created_at = %s, original = %s", updated, createdAt)
	}
	otherScope := toggle.Scope{Namespace: "billing", Environment: "production"}
	if got, err := manager.Put(ctx, otherScope, integrationFlag("checkout", true), 0); err != nil || got != 1 {
		t.Fatalf("other-scope Put revision=%d err=%v", got, err)
	}
	snapshot, err := manager.List(ctx, postgresScope)
	if err != nil || snapshot.Revision != 2 || len(snapshot.Flags) != 1 || snapshot.Flags[0].Enabled {
		t.Fatalf("updated snapshot = %+v, %v", snapshot, err)
	}
	if _, err := manager.Delete(ctx, postgresScope, "missing", 2); !errors.Is(err, toggle.ErrNotFound) {
		t.Fatalf("Delete missing error = %v", err)
	}
	snapshot, err = manager.List(ctx, postgresScope)
	if err != nil || snapshot.Revision != 2 || len(snapshot.Flags) != 1 {
		t.Fatalf("missing Delete changed snapshot = %+v, %v", snapshot, err)
	}
	if revision, err := manager.Delete(ctx, postgresScope, "checkout", 2); err != nil || revision != 3 {
		t.Fatalf("Delete revision=%d err=%v", revision, err)
	}

	reopenedStore, err := toggleent.NewStore(driver)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := toggle.NewManager(reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopened.List(ctx, postgresScope)
	if err != nil || persisted.Revision != 3 || len(persisted.Flags) != 0 {
		t.Fatalf("reopened snapshot = %+v, %v", persisted, err)
	}
}

func TestPostgresClientLoadsAndRefreshesDefinitions(t *testing.T) {
	_, _, store, manager := newPostgres(t)
	ctx := context.Background()
	if _, err := manager.Put(ctx, postgresScope, targetedFlag("user-1"), 0); err != nil {
		t.Fatal(err)
	}
	client := newEntClient(t, store, 25*time.Millisecond)
	if enabled, err := client.IsEnabled(ctx, "checkout-v2", toggle.EvaluationContext{UserID: "user-1"}, false); err != nil || !enabled {
		t.Fatalf("target user enabled=%v err=%v", enabled, err)
	}
	if enabled, err := client.IsEnabled(ctx, "checkout-v2", toggle.EvaluationContext{UserID: "user-2"}, false); err != nil || enabled {
		t.Fatalf("non-target user enabled=%v err=%v", enabled, err)
	}
	variant, err := client.GetVariant(ctx, "checkout-v2", toggle.EvaluationContext{UserID: "user-1"})
	if err != nil || !variant.Enabled || variant.Name != "new-checkout" || variant.Payload == nil || variant.Payload.Value != `{"version":2}` {
		t.Fatalf("variant = %+v, %v", variant, err)
	}
	if _, err := manager.Put(ctx, postgresScope, targetedFlag("user-2"), 1); err != nil {
		t.Fatal(err)
	}
	waitEnabled(t, client, "checkout-v2", "user-1", false)
	waitEnabled(t, client, "checkout-v2", "user-2", true)

	newClient := newEntClient(t, store, time.Hour)
	if enabled, err := newClient.IsEnabled(ctx, "checkout-v2", toggle.EvaluationContext{UserID: "user-2"}, false); err != nil || !enabled {
		t.Fatalf("reopened client enabled=%v err=%v", enabled, err)
	}
}

func TestPostgresConcurrentRevisionClaims(t *testing.T) {
	_, _, _, manager := newPostgres(t)
	ctx := context.Background()
	var successes, conflicts int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, key := range []string{"first-a", "first-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := manager.Put(ctx, postgresScope, integrationFlag(key, true), 0)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, toggle.ErrConflict):
				conflicts++
			default:
				t.Errorf("concurrent first Put: %v", err)
			}
		}(key)
	}
	wg.Wait()
	if successes != 1 || conflicts != 1 {
		t.Fatalf("first write successes=%d conflicts=%d", successes, conflicts)
	}
	snapshot, err := manager.List(ctx, postgresScope)
	if err != nil || snapshot.Revision != 1 || len(snapshot.Flags) != 1 {
		t.Fatalf("first-write snapshot = %+v, %v", snapshot, err)
	}

	successes, conflicts = 0, 0
	for _, key := range []string{"second-a", "second-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := manager.Put(ctx, postgresScope, integrationFlag(key, true), 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, toggle.ErrConflict):
				conflicts++
			default:
				t.Errorf("concurrent revision Put: %v", err)
			}
		}(key)
	}
	wg.Wait()
	if successes != 1 || conflicts != 1 {
		t.Fatalf("positive-revision writes successes=%d conflicts=%d", successes, conflicts)
	}
	snapshot, err = manager.List(ctx, postgresScope)
	if err != nil || snapshot.Revision != 2 || len(snapshot.Flags) != 2 {
		t.Fatalf("positive-revision snapshot = %+v, %v", snapshot, err)
	}
}

func TestPostgresFailedWriteRollsBackRevisionAndData(t *testing.T) {
	db, _, _, manager := newPostgres(t)
	ctx := context.Background()
	if _, err := manager.Put(ctx, postgresScope, integrationFlag("existing", true), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE toggle_flags ADD CONSTRAINT reject_toggle_key CHECK (key <> 'reject-write')`); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Put(ctx, postgresScope, integrationFlag("reject-write", true), 1); err == nil {
		t.Fatal("Put with a rejected database row unexpectedly succeeded")
	}
	snapshot, err := manager.List(ctx, postgresScope)
	if err != nil || snapshot.Revision != 1 || len(snapshot.Flags) != 1 || snapshot.Flags[0].Key != "existing" {
		t.Fatalf("after rejected Put snapshot = %+v, %v", snapshot, err)
	}
}

func TestPostgresSnapshotRevisionMatchesFlagDataDuringWrites(t *testing.T) {
	_, _, _, manager := newPostgres(t)
	ctx := context.Background()
	if _, err := manager.Put(ctx, postgresScope, integrationFlag("parity", true), 0); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	readErr := make(chan error, 1)
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snapshot, err := manager.List(ctx, postgresScope)
			if err != nil {
				select {
				case readErr <- err:
				default:
				}
				return
			}
			if len(snapshot.Flags) != 1 || snapshot.Flags[0].Enabled != (snapshot.Revision%2 == 1) {
				select {
				case readErr <- fmt.Errorf("inconsistent snapshot: %+v", snapshot):
				default:
				}
				return
			}
		}
	}()
	revision := int64(1)
	for i := 0; i < 100; i++ {
		revision++
		if _, err := manager.Put(ctx, postgresScope, integrationFlag("parity", revision%2 == 1), revision-1); err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	select {
	case err := <-readErr:
		t.Fatal(err)
	default:
	}
}

func TestPostgresStoreAdmission(t *testing.T) {
	db, _, store, manager := newPostgres(t)
	ctx := context.Background()
	if _, err := store.Put(ctx, postgresScope, toggle.Flag{Key: "bad key"}, 0); !errors.Is(err, toggle.ErrInvalidDefinition) {
		t.Fatalf("direct invalid Put error = %v", err)
	}
	if _, err := store.Put(ctx, toggle.Scope{Namespace: "bad namespace", Environment: "test"}, integrationFlag("ok", true), 0); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("direct invalid scope error = %v", err)
	}
	if _, err := store.Put(ctx, postgresScope, integrationFlag("ok", true), -1); !errors.Is(err, toggle.ErrInvalidDefinition) {
		t.Fatalf("negative revision error = %v", err)
	}
	if _, err := store.Put(ctx, postgresScope, integrationFlag("ok", true), math.MaxInt64); !errors.Is(err, toggle.ErrInvalidDefinition) {
		t.Fatalf("exhausted revision error = %v", err)
	}
	if _, err := store.Put(nil, postgresScope, integrationFlag("ok", true), 0); !errors.Is(err, toggle.ErrInvalidContext) {
		t.Fatalf("nil context error = %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO toggle_scopes(namespace, environment, revision) VALUES ('capacity', 'test', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO toggle_flags(namespace, environment, key, enabled, description, definition, created_at, updated_at)
		SELECT 'capacity', 'test', 'flag-' || n, false, '', '{"schema_version":1,"rules":[]}'::jsonb, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		FROM generate_series(0, 9999) AS n`); err != nil {
		t.Fatal(err)
	}
	capacity := toggle.Scope{Namespace: "capacity", Environment: "test"}
	if _, err := store.Put(ctx, capacity, integrationFlag("flag-0", true), 1); err != nil {
		t.Fatalf("update at capacity: %v", err)
	}
	if _, err := store.Put(ctx, capacity, integrationFlag("new-flag", true), 2); !errors.Is(err, toggle.ErrLimit) {
		t.Fatalf("new insert over capacity error = %v", err)
	}

	if _, err := manager.Put(ctx, postgresScope, integrationFlag("schema-check", true), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE toggle_flags SET definition = '{"schema_version":2,"rules":[]}'::jsonb WHERE namespace='billing' AND environment='test' AND key='schema-check'`); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.List(ctx, postgresScope); !errors.Is(err, toggle.ErrSource) {
		t.Fatalf("unknown definition version error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO toggle_scopes(namespace, environment, revision) VALUES ('invalid', 'test', -1)`); err == nil {
		t.Fatal("scope revision constraint accepted a negative value")
	}
}

var errCommitOutcomeUnknown = errors.New("commit outcome unknown")

type ambiguousCommitDriver struct {
	dialect.Driver
	commits atomic.Int32
}

func (d *ambiguousCommitDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &ambiguousCommitTx{Tx: tx, owner: d}, nil
}

type ambiguousCommitTx struct {
	dialect.Tx
	owner *ambiguousCommitDriver
}

func (tx *ambiguousCommitTx) Commit() error {
	tx.owner.commits.Add(1)
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	return errCommitOutcomeUnknown
}

func TestPostgresClientOwnershipAndAmbiguousCommit(t *testing.T) {
	_, driver, store, manager := newPostgres(t)
	ctx := context.Background()
	client := newEntClient(t, store, time.Hour)
	closeCtx, cancel := context.WithTimeout(ctx, time.Second)
	if err := client.Close(closeCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if revision, err := manager.Put(ctx, postgresScope, integrationFlag("after-close", true), 0); err != nil || revision != 1 {
		t.Fatalf("caller-owned Store after Client.Close revision=%d err=%v", revision, err)
	}

	wrapped := &ambiguousCommitDriver{Driver: driver}
	ambiguousStore, err := toggleent.NewStore(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	ambiguousManager, err := toggle.NewManager(ambiguousStore)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ambiguousManager.Put(ctx, postgresScope, integrationFlag("committed-despite-error", true), 1); !errors.Is(err, errCommitOutcomeUnknown) {
		t.Fatalf("ambiguous commit error = %v", err)
	}
	if commits := wrapped.commits.Load(); commits != 1 {
		t.Fatalf("ambiguous transaction Commit calls=%d, want exactly one", commits)
	}
	actual, err := manager.List(ctx, postgresScope)
	if err != nil || actual.Revision != 2 || len(actual.Flags) != 2 {
		t.Fatalf("fresh read after ambiguous commit = %+v, %v", actual, err)
	}
}

func readCreatedAt(t *testing.T, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, namespace, environment, key string) time.Time {
	t.Helper()
	var value time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT created_at FROM toggle_flags WHERE namespace=$1 AND environment=$2 AND key=$3`, namespace, environment, key).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
