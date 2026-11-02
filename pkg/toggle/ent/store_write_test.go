package ent_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
)

type writeDriver struct {
	tx    *writeTx
	txErr error
}

func (*writeDriver) Exec(context.Context, string, any, any) error { return nil }
func (*writeDriver) Query(context.Context, string, any, any) error {
	return errors.New("unexpected top-level query")
}
func (d *writeDriver) Tx(context.Context) (dialect.Tx, error) { return d.tx, d.txErr }
func (*writeDriver) Close() error                             { return nil }
func (*writeDriver) Dialect() string                          { return dialect.Postgres }

type writeTx struct {
	queryFailure       string
	queryErr           error
	rows               []loadRow
	rowsErr            error
	scanErr            error
	closeErr           error
	execFailure        string
	execErr            error
	nilResult          bool
	claimAffected      int64
	claimRowsErr       error
	deleteAffected     int64
	deleteRowsErr      error
	commitErr          error
	rollbackErr        error
	commits            int
	rollbacks          int
}

func (tx *writeTx) Exec(_ context.Context, query string, _ any, destination any) error {
	if tx.execFailure != "" && strings.Contains(query, tx.execFailure) {
		return tx.execErr
	}
	if tx.nilResult {
		return nil
	}
	affected, rowsErr := int64(1), error(nil)
	switch {
	case strings.Contains(query, "UPDATE toggle_scopes"):
		affected, rowsErr = tx.claimAffected, tx.claimRowsErr
	case strings.Contains(query, "DELETE FROM toggle_flags"):
		affected, rowsErr = tx.deleteAffected, tx.deleteRowsErr
	}
	result, ok := destination.(*sql.Result)
	if !ok {
		return errors.New("unexpected exec result destination")
	}
	*result = scriptedResult{affected: affected, err: rowsErr}
	return nil
}

func (tx *writeTx) Query(_ context.Context, query string, _ any, destination any) error {
	if tx.queryFailure != "" && strings.Contains(query, tx.queryFailure) {
		return tx.queryErr
	}
	rows, ok := destination.(*entsql.Rows)
	if !ok {
		return errors.New("unexpected query destination")
	}
	driver := &loadDriver{rows: tx.rows, rowsErr: tx.rowsErr, scanErr: tx.scanErr, closeErr: tx.closeErr}
	*rows = entsql.Rows{ColumnScanner: &loadRows{driver: driver}}
	return nil
}
func (tx *writeTx) Commit() error {
	tx.commits++
	return tx.commitErr
}
func (tx *writeTx) Rollback() error {
	tx.rollbacks++
	return tx.rollbackErr
}

type scriptedResult struct {
	affected int64
	err      error
}

func (r scriptedResult) LastInsertId() (int64, error) { return 0, errors.New("unsupported") }
func (r scriptedResult) RowsAffected() (int64, error) { return r.affected, r.err }

func newWriteStore(t *testing.T, driver dialect.Driver) *toggleent.Store {
	t.Helper()
	store, err := toggleent.NewStore(driver)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStorePutClaimsRevisionAndRollsBackFailedWrites(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "test"}
	newTx := func() *writeTx {
		return &writeTx{claimAffected: 1, rows: []loadRow{{count: 0}}}
	}
	backendErr := errors.New("database unavailable")
	rollbackErr := errors.New("rollback failed")
	cases := []struct {
		name      string
		configure func(*writeDriver)
		txErr     error
		flag      toggle.Flag
		want      error
		wantRev   int64
		fail      bool
		rollback  error
	}{
		{name: "successful insert", wantRev: 1},
		{name: "transaction begin failure", txErr: backendErr, want: backendErr, fail: true},
		{name: "scope insert failure", configure: func(d *writeDriver) { d.tx.execFailure, d.tx.execErr = "INSERT INTO toggle_scopes", backendErr }, want: backendErr, fail: true},
		{name: "revision claim failure", configure: func(d *writeDriver) { d.tx.execFailure, d.tx.execErr = "UPDATE toggle_scopes", backendErr }, want: backendErr, fail: true},
		{name: "revision conflict", configure: func(d *writeDriver) { d.tx.claimAffected = 0 }, want: toggle.ErrConflict, fail: true},
		{name: "revision result failure", configure: func(d *writeDriver) { d.tx.claimRowsErr = backendErr }, want: backendErr, fail: true},
		{name: "flag count query failure", configure: func(d *writeDriver) { d.tx.queryFailure, d.tx.queryErr = "SELECT COUNT(*)", backendErr }, want: backendErr, fail: true},
		{name: "flag count returned no row", configure: func(d *writeDriver) { d.tx.rows = nil }, fail: true},
		{name: "flag count fails before first row", configure: func(d *writeDriver) { d.tx.rows, d.tx.rowsErr = nil, backendErr }, want: backendErr, fail: true},
		{name: "flag count scan failure", configure: func(d *writeDriver) { d.tx.scanErr = backendErr }, want: backendErr, fail: true},
		{name: "flag count iteration failure", configure: func(d *writeDriver) { d.tx.rowsErr = backendErr }, want: backendErr, fail: true},
		{name: "capacity prevents new flag", configure: func(d *writeDriver) { d.tx.rows = []loadRow{{count: core.MaxFlagsPerSnapshot}} }, want: toggle.ErrLimit, fail: true},
		{name: "flag insert failure", configure: func(d *writeDriver) { d.tx.execFailure, d.tx.execErr = "INSERT INTO toggle_flags", backendErr }, want: backendErr, fail: true},
		{name: "driver omits SQL result", configure: func(d *writeDriver) { d.tx.nilResult = true }, fail: true},
		{name: "commit failure", configure: func(d *writeDriver) { d.tx.commitErr = backendErr }, want: backendErr, fail: true},
		{name: "rollback failure is retained", configure: func(d *writeDriver) { d.tx.execFailure, d.tx.execErr, d.tx.rollbackErr = "INSERT INTO toggle_flags", backendErr, rollbackErr }, want: backendErr, fail: true, rollback: rollbackErr},
		{name: "invalid flag is rejected before transaction", flag: toggle.Flag{Key: "bad key"}, want: toggle.ErrInvalidDefinition, fail: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tx := newTx()
			driver := &writeDriver{tx: tx, txErr: test.txErr}
			if test.configure != nil {
				test.configure(driver)
			}
			flag := test.flag
			if flag.Key == "" {
				flag = toggle.Flag{Key: "checkout", Enabled: true}
			}
			revision, err := newWriteStore(t, driver).Put(context.Background(), scope, flag, 0)
			if !test.fail {
				if err != nil || revision != test.wantRev || tx.commits != 1 || tx.rollbacks != 0 {
					t.Fatalf("Put() = (%d, %v), commits=%d rollbacks=%d", revision, err, tx.commits, tx.rollbacks)
				}
				return
			}
			if err == nil || test.want != nil && !errors.Is(err, test.want) || revision != 0 {
				t.Fatalf("Put() = (%d, %v), want error %v", revision, err, test.want)
			}
			if test.name != "transaction begin failure" && test.name != "invalid flag is rejected before transaction" && tx.rollbacks != 1 {
				t.Fatalf("failed transaction rolled back %d times, want once", tx.rollbacks)
			}
			if test.rollback != nil && !errors.Is(err, test.rollback) {
				t.Fatalf("rollback error was lost: %v", err)
			}
		})
	}
}

func TestStoreDeleteReportsMissingRowsAndDatabaseFailures(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "test"}
	backendErr := errors.New("database unavailable")
	for _, test := range []struct {
		name      string
		configure func(*writeTx)
		key       string
		want      error
		wantRev   int64
		fail      bool
	}{
		{name: "deleted", configure: func(tx *writeTx) { tx.deleteAffected = 1 }, key: "checkout", wantRev: 1},
		{name: "missing key", key: "checkout", want: toggle.ErrNotFound, fail: true},
		{name: "delete execution failure", configure: func(tx *writeTx) { tx.execFailure, tx.execErr = "DELETE FROM toggle_flags", backendErr }, key: "checkout", want: backendErr, fail: true},
		{name: "delete result failure", configure: func(tx *writeTx) { tx.deleteRowsErr = backendErr }, key: "checkout", want: backendErr, fail: true},
		{name: "invalid key", key: "bad key", want: toggle.ErrInvalidDefinition, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &writeTx{claimAffected: 1}
			if test.configure != nil {
				test.configure(tx)
			}
			revision, err := newWriteStore(t, &writeDriver{tx: tx}).Delete(context.Background(), scope, test.key, 0)
			if !test.fail {
				if err != nil || revision != test.wantRev || tx.commits != 1 || tx.rollbacks != 0 {
					t.Fatalf("Delete() = (%d, %v), commits=%d rollbacks=%d", revision, err, tx.commits, tx.rollbacks)
				}
				return
			}
			if err == nil || test.want != nil && !errors.Is(err, test.want) || revision != 0 {
				t.Fatalf("Delete() = (%d, %v), want error %v", revision, err, test.want)
			}
			if test.name != "invalid key" && tx.rollbacks != 1 {
				t.Fatalf("failed transaction rolled back %d times, want once", tx.rollbacks)
			}
		})
	}
}

func TestStoreWriteValidationRejectsCanceledContextAndUnsafeRevision(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "test"}
	store := newWriteStore(t, &writeDriver{tx: &writeTx{claimAffected: 1}})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Put(canceled, scope, toggle.Flag{Key: "checkout"}, 0); !errors.Is(err, toggle.ErrInvalidContext) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Put error = %v", err)
	}
	if _, err := store.Delete(context.Background(), scope, "checkout", int64(^uint64(0)>>1)); !errors.Is(err, toggle.ErrInvalidDefinition) {
		t.Fatalf("exhausted Delete revision error = %v", err)
	}
	if _, err := store.Put(context.Background(), toggle.Scope{Namespace: "bad scope", Environment: "test"}, toggle.Flag{Key: "checkout"}, 0); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("invalid Put scope error = %v", err)
	}
	var nilStore *toggleent.Store
	if _, err := nilStore.Put(context.Background(), scope, toggle.Flag{Key: "checkout"}, 0); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("nil Store.Put() error = %v", err)
	}
	if _, err := nilStore.Delete(context.Background(), scope, "checkout", 0); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("nil Store.Delete() error = %v", err)
	}
}
