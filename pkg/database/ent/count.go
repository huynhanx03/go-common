package ent

import (
	"context"
	"errors"
	"fmt"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

// DefaultExactCountUnder is the CountWithEstimate threshold below which the
// exact count runs.
const DefaultExactCountUnder = 10_000

// mysqlEstimatePaths are tried in order — MySQL nests the row estimate
// differently depending on the plan shape.
var mysqlEstimatePaths = []string{
	"query_block.table.rows_examined_per_scan",
	"query_block.nested_loop.0.table.rows_examined_per_scan",
	"query_block.ordering_operation.table.rows_examined_per_scan",
	"query_block.grouping_operation.table.rows_examined_per_scan",
}

// CountWithEstimate returns the number of rows matching selector, built for
// pagination totals: COUNT(*) walks every matching row, so on large tables
// it dwarfs the page query itself. This asks the query planner first
// (EXPLAIN, O(1)); when the estimate says the result is small — where users
// actually read exact totals — it runs the real COUNT, and above the
// threshold it returns the estimate (exactUnder ≤ 0 means 10k).
//
// Pass the filtered selector, before sorting and pagination are applied.
func CountWithEstimate(ctx context.Context, drv dialect.Driver, selector *sql.Selector, exactUnder int64) (int64, error) {
	if exactUnder <= 0 {
		exactUnder = DefaultExactCountUnder
	}
	if est, err := EstimateCount(ctx, drv, selector); err == nil && est >= exactUnder {
		return est, nil
	}
	// Estimation failing (unsupported dialect, unexpected plan shape) is
	// never fatal — the exact count is the correct-by-definition fallback.
	return ExactCount(ctx, drv, selector)
}

// EstimateCount returns the query planner's row estimate for selector via
// EXPLAIN. It answers in planning time (no table scan) but is only as
// accurate as the table statistics — use it where "about 1.2M results" is
// acceptable, or through CountWithEstimate for the best of both.
func EstimateCount(ctx context.Context, drv dialect.Driver, selector *sql.Selector) (int64, error) {
	query, args := selector.Query()

	switch drv.Dialect() {
	case dialect.Postgres:
		plan, err := queryOne[string](ctx, drv, "EXPLAIN (FORMAT JSON) "+query, args)
		if err != nil {
			return 0, err
		}
		if rows, ok := json.GetFloat([]byte(plan), "0.Plan.Plan Rows"); ok {
			return int64(rows), nil
		}
		return 0, errors.New("ent: no row estimate in postgres plan")

	case dialect.MySQL:
		plan, err := queryOne[string](ctx, drv, "EXPLAIN FORMAT=JSON "+query, args)
		if err != nil {
			return 0, err
		}
		for _, path := range mysqlEstimatePaths {
			if rows, ok := json.GetFloat([]byte(plan), path); ok {
				return int64(rows), nil
			}
		}
		return 0, errors.New("ent: no row estimate in mysql plan")

	default:
		return 0, fmt.Errorf("ent: EstimateCount does not support dialect %q", drv.Dialect())
	}
}

// ExactCount runs SELECT COUNT(*) over selector as a subquery, so it stays
// correct with GROUP BY or DISTINCT selections.
func ExactCount(ctx context.Context, drv dialect.Driver, selector *sql.Selector) (int64, error) {
	query, args := selector.Query()
	return queryOne[int64](ctx, drv, "SELECT COUNT(*) FROM ("+query+") AS count_rows", args)
}

// queryOne runs a query expected to return a single value.
func queryOne[T any](ctx context.Context, drv dialect.Driver, query string, args []any) (T, error) {
	var zero T

	var rows sql.Rows
	if err := drv.Query(ctx, query, args, &rows); err != nil {
		return zero, err
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, err
		}
		return zero, errors.New("ent: query returned no rows")
	}

	var v T
	if err := rows.Scan(&v); err != nil {
		return zero, err
	}
	return v, nil
}
