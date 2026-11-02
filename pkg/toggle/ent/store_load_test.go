package ent_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
)

// These scripted rows cover database failures and corrupt persisted snapshots
// that cannot be produced deterministically by a healthy PostgreSQL instance.
type loadDriver struct {
	queryErr error
	rows     []loadRow
	rowsErr  error
	scanErr  error
	closeErr error
	onScan   func()
}

func (*loadDriver) Exec(context.Context, string, any, any) error { return nil }
func (d *loadDriver) Query(_ context.Context, _ string, _ any, destination any) error {
	if d.queryErr != nil {
		return d.queryErr
	}
	rows, ok := destination.(*entsql.Rows)
	if !ok {
		return errors.New("unexpected query destination")
	}
	*rows = entsql.Rows{ColumnScanner: &loadRows{driver: d}}
	return nil
}
func (*loadDriver) Tx(context.Context) (dialect.Tx, error) { return nil, nil }
func (*loadDriver) Close() error                          { return nil }
func (*loadDriver) Dialect() string                       { return dialect.Postgres }

type loadRow struct {
	revision        int64
	count           int64
	exists          bool
	key             sql.NullString
	description     sql.NullString
	enabled         sql.NullBool
	definition      sql.NullString
	definitionBytes sql.NullInt64
}

func validLoadRow(revision int64, key string) loadRow {
	definition := `{"schema_version":1,"rules":[]}`
	return loadRow{
		revision: revision,
		key: sql.NullString{String: key, Valid: true},
		description: sql.NullString{Valid: true},
		enabled: sql.NullBool{Valid: true},
		definition: sql.NullString{String: definition, Valid: true},
		definitionBytes: sql.NullInt64{Int64: int64(len(definition)), Valid: true},
	}
}

type loadRows struct {
	driver  *loadDriver
	current int
}

func (r *loadRows) Close() error                          { return r.driver.closeErr }
func (*loadRows) ColumnTypes() ([]*sql.ColumnType, error) { return nil, nil }
func (*loadRows) Columns() ([]string, error)              { return nil, nil }
func (r *loadRows) Err() error                            { return r.driver.rowsErr }
func (*loadRows) NextResultSet() bool                     { return false }
func (r *loadRows) Next() bool {
	if r.current == len(r.driver.rows) {
		return false
	}
	r.current++
	return true
}
func (r *loadRows) Scan(destinations ...any) error {
	if r.driver.scanErr != nil {
		return r.driver.scanErr
	}
	if r.driver.onScan != nil {
		r.driver.onScan()
	}
	row := r.driver.rows[r.current-1]
	if len(destinations) == 2 {
		*destinations[0].(*int64) = row.count
		*destinations[1].(*bool) = row.exists
		return nil
	}
	*destinations[0].(*int64) = row.revision
	*destinations[1].(*sql.NullString) = row.key
	*destinations[2].(*sql.NullString) = row.description
	*destinations[3].(*sql.NullBool) = row.enabled
	*destinations[4].(*sql.NullString) = row.definition
	*destinations[5].(*sql.NullInt64) = row.definitionBytes
	return nil
}

func newLoadStore(t *testing.T, driver *loadDriver) *toggleent.Store {
	t.Helper()
	store, err := toggleent.NewStore(driver)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreLoadValidatesScopeAndReturnsConsistentSnapshots(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "test"}
	for _, test := range []struct {
		name string
		rows []loadRow
		want int
	}{
		{name: "missing scope", rows: nil},
		{name: "empty existing scope", rows: []loadRow{{revision: 2}}},
		{name: "one flag", rows: []loadRow{validLoadRow(3, "checkout")}, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newLoadStore(t, &loadDriver{rows: test.rows})
			snapshot, err := store.Load(context.Background(), scope)
			if err != nil || snapshot.Scope != scope || len(snapshot.Flags) != test.want {
				t.Fatalf("Load() = %+v, %v", snapshot, err)
			}
			if test.name == "empty existing scope" && snapshot.Revision != 2 {
				t.Fatalf("empty scope revision = %d, want 2", snapshot.Revision)
			}
		})
	}
	var nilStore *toggleent.Store
	if _, err := nilStore.Load(context.Background(), scope); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("nil Store.Load() error = %v", err)
	}
	store := newLoadStore(t, &loadDriver{})
	if _, err := store.Load(nil, scope); !errors.Is(err, toggle.ErrInvalidContext) {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := store.Load(context.Background(), toggle.Scope{Namespace: "bad scope", Environment: "test"}); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("invalid scope error = %v", err)
	}
}

func TestStoreLoadClassifiesDatabaseAndCorruptDataFailures(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "test"}
	queryErr := errors.New("backend credentials")
	rowsErr := errors.New("connection dropped while reading")
	scanErr := errors.New("column conversion failed")
	closeErr := errors.New("result close failed")
	invalidDefinition := validLoadRow(1, "checkout")
	invalidDefinition.definition = sql.NullString{String: `{`, Valid: true}
	invalidVersion := validLoadRow(1, "checkout")
	invalidVersion.definition = sql.NullString{String: `{"schema_version":2,"rules":[]}`, Valid: true}
	missingDescription := validLoadRow(1, "checkout")
	missingDescription.description = sql.NullString{}
	invalidKey := validLoadRow(1, "bad key")
	missingDefinition := validLoadRow(1, "checkout")
	missingDefinition.definition = sql.NullString{}
	oversizedDefinition := validLoadRow(1, "checkout")
	oversizedDefinition.definitionBytes = sql.NullInt64{Int64: core.MaxDefinitionSize + 1, Valid: true}
	badRows := make([]loadRow, 2)
	badRows[0] = validLoadRow(1, "first")
	badRows[1] = validLoadRow(2, "second")

	for _, test := range []struct {
		name       string
		driver     *loadDriver
		want       error
		wantSource bool
	}{
		{name: "query error", driver: &loadDriver{queryErr: queryErr}, want: queryErr},
		{name: "scan error", driver: &loadDriver{rows: []loadRow{validLoadRow(1, "checkout")}, scanErr: scanErr}, want: scanErr},
		{name: "row iteration error", driver: &loadDriver{rowsErr: rowsErr}, want: rowsErr},
		{name: "close error", driver: &loadDriver{rows: []loadRow{{revision: 1}}, closeErr: closeErr}, want: closeErr},
		{name: "revision mismatch", driver: &loadDriver{rows: badRows}, want: toggle.ErrInvalidDefinition, wantSource: true},
		{name: "missing description", driver: &loadDriver{rows: []loadRow{missingDescription}}, want: toggle.ErrInvalidDefinition, wantSource: true},
		{name: "invalid flag definition", driver: &loadDriver{rows: []loadRow{invalidKey}}, want: toggle.ErrInvalidDefinition, wantSource: true},
		{name: "definition exceeds limit", driver: &loadDriver{rows: []loadRow{oversizedDefinition}}, want: toggle.ErrLimit, wantSource: true},
		{name: "missing definition", driver: &loadDriver{rows: []loadRow{missingDefinition}}, want: toggle.ErrInvalidDefinition, wantSource: true},
		{name: "malformed definition", driver: &loadDriver{rows: []loadRow{invalidDefinition}}, want: toggle.ErrInvalidDefinition, wantSource: true},
		{name: "unsupported definition version", driver: &loadDriver{rows: []loadRow{invalidVersion}}, want: toggle.ErrInvalidDefinition, wantSource: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newLoadStore(t, test.driver)
			_, err := store.Load(context.Background(), scope)
			if !errors.Is(err, test.want) || errors.Is(err, toggle.ErrSource) != test.wantSource {
				t.Fatalf("Load() error = %v, want source=%v and cause=%v", err, test.wantSource, test.want)
			}
			if test.want == queryErr || test.want == scanErr || test.want == rowsErr || test.want == closeErr {
				if strings.Contains(err.Error(), test.want.Error()) {
					t.Fatalf("storage error exposed backend details: %v", err)
				}
			}
		})
	}
}

func TestStoreLoadEnforcesSnapshotBoundsAndContextCancellation(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "test"}
	tooMany := make([]loadRow, core.MaxFlagsPerSnapshot+1)
	for i := range tooMany {
		tooMany[i] = validLoadRow(1, fmt.Sprintf("flag-%d", i))
	}
	store := newLoadStore(t, &loadDriver{rows: tooMany})
	if _, err := store.Load(context.Background(), scope); !errors.Is(err, toggle.ErrSource) || !errors.Is(err, toggle.ErrLimit) {
		t.Fatalf("flag count limit error = %v", err)
	}

	largeRows := make([]loadRow, 4200)
	for i := range largeRows {
		row := validLoadRow(1, fmt.Sprintf("flag-%05d", i))
		row.description = sql.NullString{String: strings.Repeat("x", core.MaxFlagDescriptionLength), Valid: true}
		largeRows[i] = row
	}
	store = newLoadStore(t, &loadDriver{rows: largeRows})
	if _, err := store.Load(context.Background(), scope); !errors.Is(err, toggle.ErrSource) || !errors.Is(err, toggle.ErrLimit) {
		t.Fatalf("snapshot byte limit error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	driver := &loadDriver{rows: []loadRow{validLoadRow(1, "checkout")}}
	driver.onScan = cancel
	store = newLoadStore(t, driver)
	if _, err := store.Load(ctx, scope); !errors.Is(err, toggle.ErrInvalidContext) || !errors.Is(err, context.Canceled) {
		t.Fatalf("post-query cancellation error = %v", err)
	}
}
