package ent

import (
	"context"
	stdsql "database/sql"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
)

// fakeScanRows is a single-row ColumnScanner yielding one value.
type fakeScanRows struct {
	value any
	done  bool
}

func (r *fakeScanRows) Close() error                               { return nil }
func (r *fakeScanRows) ColumnTypes() ([]*stdsql.ColumnType, error) { return nil, nil }
func (r *fakeScanRows) Columns() ([]string, error)                 { return nil, nil }
func (r *fakeScanRows) Err() error                                 { return nil }
func (r *fakeScanRows) NextResultSet() bool                        { return false }
func (r *fakeScanRows) Next() bool                                 { first := !r.done; r.done = true; return first }
func (r *fakeScanRows) Scan(dest ...any) error {
	switch d := dest[0].(type) {
	case *string:
		*d = r.value.(string)
	case *int64:
		*d = r.value.(int64)
	}
	return nil
}

// fakeCountDriver answers EXPLAIN with a canned plan and everything else
// with a canned count; it records which queries ran.
type fakeCountDriver struct {
	dialectName string
	plan        string
	count       int64
	queries     []string
}

func (d *fakeCountDriver) Exec(context.Context, string, any, any) error { return nil }
func (d *fakeCountDriver) Tx(context.Context) (dialect.Tx, error)       { return nil, nil }
func (d *fakeCountDriver) Close() error                                 { return nil }
func (d *fakeCountDriver) Dialect() string                              { return d.dialectName }
func (d *fakeCountDriver) Query(_ context.Context, query string, _, v any) error {
	d.queries = append(d.queries, query)
	rows := v.(*sql.Rows)
	if strings.HasPrefix(query, "EXPLAIN") {
		*rows = sql.Rows{ColumnScanner: &fakeScanRows{value: d.plan}}
	} else {
		*rows = sql.Rows{ColumnScanner: &fakeScanRows{value: d.count}}
	}
	return nil
}

func countSelector() *sql.Selector {
	return sql.Dialect(dialect.Postgres).Select("*").From(sql.Table("users"))
}

func TestEstimateCountPostgres(t *testing.T) {
	drv := &fakeCountDriver{
		dialectName: dialect.Postgres,
		plan:        `[{"Plan": {"Node Type": "Seq Scan", "Plan Rows": 123456}}]`,
	}
	got, err := EstimateCount(context.Background(), drv, countSelector())
	if err != nil || got != 123456 {
		t.Fatalf("estimate = %d, %v", got, err)
	}
}

func TestEstimateCountMySQL(t *testing.T) {
	drv := &fakeCountDriver{
		dialectName: dialect.MySQL,
		plan:        `{"query_block": {"table": {"rows_examined_per_scan": 5000}}}`,
	}
	got, err := EstimateCount(context.Background(), drv, countSelector())
	if err != nil || got != 5000 {
		t.Fatalf("estimate = %d, %v", got, err)
	}
}

func TestEstimateCountUnsupportedDialect(t *testing.T) {
	drv := &fakeCountDriver{dialectName: dialect.SQLite}
	if _, err := EstimateCount(context.Background(), drv, countSelector()); err == nil {
		t.Fatal("want error for unsupported dialect")
	}
}

func TestCountWithEstimateUsesEstimateWhenLarge(t *testing.T) {
	drv := &fakeCountDriver{
		dialectName: dialect.Postgres,
		plan:        `[{"Plan": {"Plan Rows": 2000000}}]`,
		count:       42, // must not be reached
	}
	got, err := CountWithEstimate(context.Background(), drv, countSelector(), 10_000)
	if err != nil || got != 2000000 {
		t.Fatalf("count = %d, %v", got, err)
	}
	if len(drv.queries) != 1 {
		t.Fatalf("exact COUNT must be skipped, ran: %v", drv.queries)
	}
}

func TestCountWithEstimateExactWhenSmall(t *testing.T) {
	drv := &fakeCountDriver{
		dialectName: dialect.Postgres,
		plan:        `[{"Plan": {"Plan Rows": 37}}]`,
		count:       42,
	}
	got, err := CountWithEstimate(context.Background(), drv, countSelector(), 10_000)
	if err != nil || got != 42 {
		t.Fatalf("count = %d, %v", got, err)
	}
}

func TestCountWithEstimateFallsBackOnBadPlan(t *testing.T) {
	drv := &fakeCountDriver{
		dialectName: dialect.Postgres,
		plan:        `not json at all`,
		count:       42,
	}
	got, err := CountWithEstimate(context.Background(), drv, countSelector(), 10_000)
	if err != nil || got != 42 {
		t.Fatalf("count = %d, %v", got, err)
	}
}
