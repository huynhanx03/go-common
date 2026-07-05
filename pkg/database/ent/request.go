package ent

import (
	"regexp"

	"entgo.io/ent/dialect/sql"

	"github.com/huynhanx03/go-common/pkg/dto"
	"github.com/huynhanx03/go-common/pkg/utils"
)

// Filter types for dto.SearchFilter.Type. Unknown or empty types fall back
// to equality.
const (
	SearchTypeSearch = "search" // case-insensitive contains
	SearchTypeExact  = "exact"  // IN (single value or list)
	SearchTypeFilter = "filter" // alias of exact

	OpEQ      = "eq"
	OpNEQ     = "neq"
	OpGT      = "gt"
	OpGTE     = "gte"
	OpLT      = "lt"
	OpLTE     = "lte"
	OpIn      = "in"
	OpNotIn   = "not_in"
	OpIsNull  = "is_null"
	OpNotNull = "is_not_null"
)

// filterPredicates maps a filter type to its predicate builder — add a row
// to support a new operator. A builder returning nil skips the filter.
var filterPredicates = map[string]func(col string, v any) *sql.Predicate{
	SearchTypeSearch: func(col string, v any) *sql.Predicate {
		if s, ok := v.(string); ok && s != "" {
			return sql.ContainsFold(col, s)
		}
		return nil
	},
	SearchTypeExact:  inPredicate,
	SearchTypeFilter: inPredicate,
	OpIn:             inPredicate,
	OpNotIn: func(col string, v any) *sql.Predicate {
		return sql.NotIn(col, listOf(v)...)
	},
	OpEQ:      func(col string, v any) *sql.Predicate { return sql.EQ(col, v) },
	OpNEQ:     func(col string, v any) *sql.Predicate { return sql.NEQ(col, v) },
	OpGT:      func(col string, v any) *sql.Predicate { return sql.GT(col, v) },
	OpGTE:     func(col string, v any) *sql.Predicate { return sql.GTE(col, v) },
	OpLT:      func(col string, v any) *sql.Predicate { return sql.LT(col, v) },
	OpLTE:     func(col string, v any) *sql.Predicate { return sql.LTE(col, v) },
	OpIsNull:  func(col string, _ any) *sql.Predicate { return sql.IsNull(col) },
	OpNotNull: func(col string, _ any) *sql.Predicate { return sql.NotNull(col) },
}

func inPredicate(col string, v any) *sql.Predicate {
	return sql.In(col, listOf(v)...)
}

// listOf normalizes a filter value into IN-clause arguments.
func listOf(v any) []any {
	if vs, ok := v.([]any); ok {
		return vs
	}
	return []any{v}
}

// columnPattern accepts plain identifiers only — anything else (spaces,
// quotes, dots) could smuggle SQL through the column position, which is not
// parameterized.
var columnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// safeColumn normalizes a client-supplied key to snake_case and rejects
// anything that is not a plain identifier or is outside the whitelist.
func safeColumn(key string, allowed map[string]struct{}) (string, bool) {
	col := utils.ToSnakeCase(key)
	if !columnPattern.MatchString(col) {
		return "", false
	}
	if allowed != nil {
		if _, ok := allowed[col]; !ok {
			return "", false
		}
	}
	return col, true
}

// columnSet builds the whitelist lookup; nil (allow any valid identifier)
// when no columns are given.
func columnSet(columns []string) map[string]struct{} {
	if len(columns) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(columns))
	for _, c := range columns {
		set[c] = struct{}{}
	}
	return set
}

// ApplyQueryOptions applies all standard query options (Filter, Sort,
// Pagination). Pass allowedColumns to restrict which columns clients may
// filter and sort by — recommended for any externally-facing endpoint;
// without it any well-formed identifier is accepted.
func ApplyQueryOptions(opts *dto.QueryOptions, selector *sql.Selector, allowedColumns ...string) {
	if opts == nil {
		return
	}
	allowed := columnSet(allowedColumns)
	applyFilters(opts.Filters, selector, allowed)
	applySort(opts.Sort, selector, allowed)
	ApplyPagination(opts.Pagination, selector)
}

// ApplyFilters applies dynamic filters based on dto.SearchFilter options.
// Filters on invalid or non-whitelisted columns are skipped.
func ApplyFilters(filters []dto.SearchFilter, selector *sql.Selector, allowedColumns ...string) {
	applyFilters(filters, selector, columnSet(allowedColumns))
}

func applyFilters(filters []dto.SearchFilter, selector *sql.Selector, allowed map[string]struct{}) {
	for _, f := range filters {
		if f.Key == "" {
			continue
		}
		// Null checks are the only filters that carry no value.
		if f.Value == nil && f.Type != OpIsNull && f.Type != OpNotNull {
			continue
		}
		col, ok := safeColumn(f.Key, allowed)
		if !ok {
			continue
		}

		build, known := filterPredicates[f.Type]
		if !known {
			build = filterPredicates[OpEQ]
		}
		if p := build(col, f.Value); p != nil {
			selector.Where(p)
		}
	}
}

// ApplySort applies sorting. Sorts on invalid or non-whitelisted columns are
// skipped; when the selector ends up with no ordering at all it falls back
// to id DESC, so paginated results stay stable across pages.
func ApplySort(sorts []dto.SortOption, selector *sql.Selector, allowedColumns ...string) {
	applySort(sorts, selector, columnSet(allowedColumns))
}

func applySort(sorts []dto.SortOption, selector *sql.Selector, allowed map[string]struct{}) {
	for _, sort := range sorts {
		if sort.Key == "" {
			continue
		}
		col, ok := safeColumn(sort.Key, allowed)
		if !ok {
			continue
		}

		if sort.Order == -1 {
			selector.OrderBy(sql.Desc(col))
		} else {
			selector.OrderBy(sql.Asc(col))
		}
	}

	if len(selector.OrderColumns()) == 0 {
		selector.OrderBy(sql.Desc("id"))
	}
}

// ApplyPagination applies pagination. When a cursor is present it uses
// keyset pagination — WHERE id < cursor instead of OFFSET — which stays O(1)
// at any depth, while OFFSET n scans and discards n rows. Pass the last row's
// id of the previous page as the cursor; it pairs with the default id DESC
// ordering (UUIDv7 ids sort chronologically, so pages walk newest → oldest).
// Cursor and custom sorts don't mix — with a cursor, leave Sort empty.
func ApplyPagination(pagination *dto.PaginationOptions, selector *sql.Selector) {
	if pagination == nil {
		return
	}
	pagination.SetDefaults()
	selector.Limit(pagination.PageSize)

	if pagination.Cursor != nil && pagination.Cursor != "" {
		selector.Where(sql.LT("id", pagination.Cursor))
		return
	}
	selector.Offset((pagination.Page - 1) * pagination.PageSize)
}
