package ent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/i18n"
)

type driverStub struct {
	dialect   string
	queryErr  error
	execErr   error
	rows      *rowsStub
	result    sql.Result
	lastQuery string
	lastArgs  any
}

type valueDriverStub struct{}

func (valueDriverStub) Exec(context.Context, string, any, any) error  { return nil }
func (valueDriverStub) Query(context.Context, string, any, any) error { return nil }
func (valueDriverStub) Tx(context.Context) (dialect.Tx, error)        { return nil, nil }
func (valueDriverStub) Close() error                                  { return nil }
func (valueDriverStub) Dialect() string                               { return dialect.Postgres }

func (d *driverStub) Exec(_ context.Context, query string, args, target any) error {
	d.lastQuery, d.lastArgs = query, args
	if d.execErr != nil {
		return d.execErr
	}
	*target.(*sql.Result) = d.result
	return nil
}

func (d *driverStub) Query(_ context.Context, query string, args, target any) error {
	d.lastQuery, d.lastArgs = query, args
	if d.queryErr != nil {
		return d.queryErr
	}
	*target.(*entsql.Rows) = entsql.Rows{ColumnScanner: d.rows}
	return nil
}

func (*driverStub) Tx(context.Context) (dialect.Tx, error) { return nil, nil }
func (*driverStub) Close() error                           { return nil }
func (d *driverStub) Dialect() string                      { return d.dialect }

type rowsStub struct {
	values   [][]any
	position int
	rowErr   error
	scanErr  error
	closeErr error
}

func (r *rowsStub) Close() error                          { return r.closeErr }
func (*rowsStub) ColumnTypes() ([]*sql.ColumnType, error) { return nil, nil }
func (*rowsStub) Columns() ([]string, error)              { return nil, nil }
func (r *rowsStub) Err() error                            { return r.rowErr }
func (r *rowsStub) Next() bool {
	if r.position >= len(r.values) {
		return false
	}
	r.position++
	return true
}
func (*rowsStub) NextResultSet() bool { return false }
func (r *rowsStub) Scan(destinations ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.values[r.position-1]
	for index, destination := range destinations {
		value := row[index]
		switch target := destination.(type) {
		case *int64:
			*target = value.(int64)
		case *sql.NullString:
			if value == nil {
				*target = sql.NullString{}
			} else {
				*target = sql.NullString{String: value.(string), Valid: true}
			}
		case *sql.NullInt64:
			if value == nil {
				*target = sql.NullInt64{}
			} else {
				*target = sql.NullInt64{Int64: value.(int64), Valid: true}
			}
		case *string:
			*target = value.(string)
		default:
			return fmt.Errorf("unsupported scan destination %T", destination)
		}
	}
	return nil
}

type resultStub struct {
	rows int64
	err  error
}

func (r resultStub) LastInsertId() (int64, error) { return 0, nil }
func (r resultStub) RowsAffected() (int64, error) { return r.rows, r.err }

func postgresStore(t *testing.T, driver *driverStub) *Store {
	t.Helper()
	driver.dialect = dialect.Postgres
	store, err := NewStore(driver)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func validState(domain string, revision int64) i18n.State {
	return i18n.State{Draft: i18n.Catalog{
		Domain: domain, Revision: revision,
		DomainConfig: i18n.DomainConfig{DefaultLocale: "en", Locales: []string{"en"}},
		Keys:         []i18n.Key{},
	}}
}

func stateJSON(t *testing.T, state i18n.State) string {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func catalogRow(revision int64, encoded any, size any) *rowsStub {
	return &rowsStub{values: [][]any{{revision, encoded, size}}}
}

func TestNewStoreAndEveryOperationRejectInvalidInvocation(t *testing.T) {
	if _, err := NewStore(nil); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil driver error=%v", err)
	}
	wrongDialect := &driverStub{dialect: dialect.MySQL}
	if _, err := NewStore(wrongDialect); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("non-Postgres driver error=%v", err)
	}
	var typedNil *driverStub
	if _, err := NewStore(typedNil); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("typed nil driver error=%v", err)
	}
	if _, err := NewStore(valueDriverStub{}); err != nil {
		t.Fatalf("value driver rejected: %v", err)
	}

	var nilStore *Store
	if _, err := nilStore.Read(context.Background(), "billing"); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil store read error=%v", err)
	}
	if err := nilStore.CompareAndSwap(context.Background(), "billing", 0, validState("billing", 1)); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil store write error=%v", err)
	}
	if err := nilStore.Delete(context.Background(), "billing", 1); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil store delete error=%v", err)
	}
	if _, err := nilStore.Domains(context.Background()); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil store domains error=%v", err)
	}

	store := postgresStore(t, &driverStub{})
	if _, err := store.Read(nil, "billing"); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil request context error=%v", err)
	}
	if _, err := store.Read(context.Background(), "bad domain"); !errors.Is(err, i18n.ErrInvalidDefinition) {
		t.Fatalf("invalid domain read error=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Domains(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inventory request error=%v", err)
	}
	if _, err := store.Read(canceled, "billing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read error=%v", err)
	}
	if err := store.CompareAndSwap(nil, "billing", 0, validState("billing", 1)); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil write context error=%v", err)
	}
	if err := store.Delete(context.Background(), "bad domain", 1); !errors.Is(err, i18n.ErrInvalidDefinition) {
		t.Fatalf("invalid delete domain error=%v", err)
	}
	if err := store.Delete(context.Background(), "billing", 0); !errors.Is(err, i18n.ErrConflict) {
		t.Fatalf("zero delete revision error=%v", err)
	}
	if err := store.Delete(context.Background(), "billing", int64(^uint64(0)>>1)); !errors.Is(err, i18n.ErrConflict) {
		t.Fatalf("exhausted delete revision error=%v", err)
	}
}

func TestReadReturnsValidatedOwnedStateAndRedactsStorageFailures(t *testing.T) {
	state := validState("billing", 3)
	encoded := stateJSON(t, state)
	store := postgresStore(t, &driverStub{rows: catalogRow(3, encoded, int64(len(encoded)))})
	got, err := store.Read(context.Background(), "billing")
	if err != nil || got.Draft.Revision != 3 || got.Draft.Domain != "billing" {
		t.Fatalf("catalog read=%+v err=%v", got, err)
	}
	got.Draft.Locales[0] = "changed"
	if state.Draft.Locales[0] != "en" {
		t.Fatal("read result aliases persisted state")
	}

	secret := errors.New("postgres password=hunter2")
	cases := []struct {
		name   string
		driver *driverStub
		want   error
	}{
		{name: "query error", driver: &driverStub{queryErr: secret}, want: i18n.ErrSource},
		{name: "no row", driver: &driverStub{rows: &rowsStub{}}, want: i18n.ErrNotFound},
		{name: "iteration error", driver: &driverStub{rows: &rowsStub{rowErr: secret}}, want: i18n.ErrSource},
		{name: "scan error", driver: &driverStub{rows: &rowsStub{values: [][]any{{int64(1), "{}", int64(2)}}, scanErr: secret}}, want: i18n.ErrSource},
		{name: "duplicate domain row", driver: &driverStub{rows: &rowsStub{values: [][]any{{int64(1), "{}", int64(2)}, {int64(1), "{}", int64(2)}}}}, want: i18n.ErrInvalidDefinition},
		{name: "iteration error after scan", driver: &driverStub{rows: &rowsStub{values: [][]any{{int64(1), "{}", int64(2)}}, rowErr: secret}}, want: i18n.ErrSource},
		{name: "close error", driver: &driverStub{rows: &rowsStub{values: [][]any{{int64(1), "{}", int64(2)}}, closeErr: secret}}, want: i18n.ErrSource},
		{name: "oversized row", driver: &driverStub{rows: catalogRow(1, "{}", int64(MaxJSONBTextBytes+1))}, want: i18n.ErrLimit},
		{name: "missing encoded state", driver: &driverStub{rows: catalogRow(1, nil, int64(2))}, want: i18n.ErrInvalidDefinition},
		{name: "invalid revision", driver: &driverStub{rows: catalogRow(0, "{}", int64(2))}, want: i18n.ErrInvalidDefinition},
		{name: "malformed JSON", driver: &driverStub{rows: catalogRow(1, "{", int64(1))}, want: i18n.ErrInvalidDefinition},
		{name: "revision column disagrees with payload", driver: &driverStub{rows: catalogRow(2, encoded, int64(len(encoded)))}, want: i18n.ErrInvalidDefinition},
		{name: "invalid catalog payload", driver: &driverStub{rows: catalogRow(3, stateJSON(t, validState("other", 3)), int64(len(encoded)))}, want: i18n.ErrSource},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := postgresStore(t, test.driver)
			_, err := store.Read(context.Background(), "billing")
			if !errors.Is(err, test.want) {
				t.Fatalf("read error=%v, want category %v", err, test.want)
			}
			if test.want == i18n.ErrSource && strings.Contains(fmt.Sprint(err), secret.Error()) {
				t.Fatalf("storage details leaked: %v", err)
			}
		})
	}
}

func TestCompareAndSwapUsesExpectedRevisionAndMapsDatabaseOutcomes(t *testing.T) {
	ctx := context.Background()
	store := postgresStore(t, &driverStub{result: resultStub{rows: 1}})
	if err := store.CompareAndSwap(ctx, "billing", 0, validState("billing", 1)); err != nil {
		t.Fatalf("catalog create failed: %v", err)
	}
	if !strings.HasPrefix(store.driver.(*driverStub).lastQuery, "INSERT") {
		t.Fatalf("initial write did not create: %q", store.driver.(*driverStub).lastQuery)
	}
	if err := store.CompareAndSwap(ctx, "billing", 1, validState("billing", 2)); err != nil {
		t.Fatalf("catalog update failed: %v", err)
	}
	if !strings.HasPrefix(store.driver.(*driverStub).lastQuery, "UPDATE") {
		t.Fatalf("revisioned write did not update: %q", store.driver.(*driverStub).lastQuery)
	}
	for _, test := range []struct {
		expected int64
		next     int64
	}{
		{expected: -1, next: 0},
		{expected: int64(^uint64(0) >> 1), next: int64(^uint64(0) >> 1)},
		{expected: 1, next: 4},
	} {
		if err := store.CompareAndSwap(ctx, "billing", test.expected, validState("billing", test.next)); !errors.Is(err, i18n.ErrConflict) {
			t.Errorf("invalid CAS expected=%d next=%d error=%v", test.expected, test.next, err)
		}
	}
	if err := store.CompareAndSwap(ctx, "billing", 0, validState("other", 1)); !errors.Is(err, i18n.ErrInvalidDefinition) {
		t.Fatalf("wrong-domain state accepted: %v", err)
	}
	invalid := validState("billing", 1)
	invalid.Draft.Locales = []string{"not a locale"}
	if err := store.CompareAndSwap(ctx, "billing", 0, invalid); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("invalid state accepted: %v", err)
	}

	secret := errors.New("write failed with private details")
	for _, driver := range []*driverStub{
		{execErr: secret},
		{result: nil},
		{result: resultStub{err: secret}},
		{result: resultStub{rows: 0}},
	} {
		store := postgresStore(t, driver)
		err := store.CompareAndSwap(ctx, "billing", 0, validState("billing", 1))
		switch {
		case errors.Is(err, i18n.ErrConflict):
			if driver.result == nil || driver.execErr != nil {
				t.Fatalf("database failure mapped as conflict: %v", err)
			}
		case errors.Is(err, i18n.ErrSource):
			if errors.Is(err, i18n.ErrConflict) {
				t.Fatalf("storage failure also reported conflict: %v", err)
			}
		default:
			t.Fatalf("unexpected CAS outcome: %v", err)
		}
	}
}

func TestDeleteAndDomainInventoryRespectOptimisticConcurrencyAndLimits(t *testing.T) {
	ctx := context.Background()
	store := postgresStore(t, &driverStub{result: resultStub{rows: 1}})
	if err := store.Delete(ctx, "billing", 2); err != nil {
		t.Fatalf("revisioned delete failed: %v", err)
	}
	if !strings.HasPrefix(store.driver.(*driverStub).lastQuery, "DELETE") {
		t.Fatalf("unexpected delete query: %q", store.driver.(*driverStub).lastQuery)
	}
	if err := postgresStore(t, &driverStub{result: resultStub{rows: 0}}).Delete(ctx, "billing", 2); !errors.Is(err, i18n.ErrConflict) {
		t.Fatalf("stale delete error=%v", err)
	}
	for _, driver := range []*driverStub{
		{execErr: errors.New("delete failed")},
		{result: nil},
		{result: resultStub{err: errors.New("rows affected failed")}},
	} {
		if err := postgresStore(t, driver).Delete(ctx, "billing", 2); !errors.Is(err, i18n.ErrSource) {
			t.Errorf("delete storage failure=%v", err)
		}
	}

	domains := postgresStore(t, &driverStub{rows: &rowsStub{values: [][]any{{"alpha"}, {"beta"}}}})
	got, err := domains.Domains(ctx)
	if err != nil || fmt.Sprint(got) != "[alpha beta]" {
		t.Fatalf("domain inventory=%v err=%v", got, err)
	}
	if _, err := postgresStore(t, &driverStub{queryErr: errors.New("db unavailable")}).Domains(ctx); !errors.Is(err, i18n.ErrSource) {
		t.Fatalf("domain query failure=%v", err)
	}
	if _, err := postgresStore(t, &driverStub{rows: &rowsStub{values: [][]any{{"bad domain"}}}}).Domains(ctx); !errors.Is(err, i18n.ErrSource) || !errors.Is(err, i18n.ErrInvalidDefinition) {
		t.Fatalf("invalid persisted domain=%v", err)
	}
	if _, err := postgresStore(t, &driverStub{rows: &rowsStub{values: [][]any{{"alpha"}}, scanErr: errors.New("bad scan")}}).Domains(ctx); !errors.Is(err, i18n.ErrSource) {
		t.Fatalf("inventory scan failure=%v", err)
	}
	if _, err := postgresStore(t, &driverStub{rows: &rowsStub{values: [][]any{{"alpha"}}, rowErr: errors.New("cursor failed")}}).Domains(ctx); !errors.Is(err, i18n.ErrSource) {
		t.Fatalf("inventory iteration failure=%v", err)
	}
	if _, err := postgresStore(t, &driverStub{rows: &rowsStub{values: [][]any{{"alpha"}}, closeErr: errors.New("close failed")}}).Domains(ctx); !errors.Is(err, i18n.ErrSource) {
		t.Fatalf("inventory close failure=%v", err)
	}
	var nilStore *Store
	if _, err := nilStore.Domains(ctx); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil store inventory=%v", err)
	}
	if _, err := domains.Domains(nil); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil context inventory=%v", err)
	}
	tooMany := make([][]any, i18n.MaxDomains+1)
	for index := range tooMany {
		tooMany[index] = []any{fmt.Sprintf("domain-%04d", index)}
	}
	if _, err := postgresStore(t, &driverStub{rows: &rowsStub{values: tooMany}}).Domains(ctx); !errors.Is(err, i18n.ErrLimit) {
		t.Fatalf("unbounded domain inventory error=%v", err)
	}
}

func TestStorageFailureKeepsCauseForTracingWithoutLeakingItToText(t *testing.T) {
	secret := errors.New("password=private")
	for _, err := range []error{storageFailure(secret), sourceFailure(secret)} {
		if !errors.Is(err, i18n.ErrSource) || !errors.Is(err, secret) {
			t.Fatalf("storage category or underlying cause lost: %v", err)
		}
		if strings.Contains(err.Error(), secret.Error()) {
			t.Fatalf("database detail exposed: %v", err)
		}
	}
	if storageFailure(nil) != nil {
		t.Fatal("nil database error was wrapped")
	}
}

var _ dialect.Driver = (*driverStub)(nil)
var _ entsql.ColumnScanner = (*rowsStub)(nil)
