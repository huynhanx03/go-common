package ent

import (
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"

	"github.com/huynhanx03/go-common/pkg/dto"
)

func newSelector() *sql.Selector {
	return sql.Dialect(dialect.MySQL).Select("*").From(sql.Table("users"))
}

func render(t *testing.T, s *sql.Selector) (string, []any) {
	t.Helper()
	query, args := s.Query()
	return query, args
}

func TestApplyFiltersOperators(t *testing.T) {
	cases := []struct {
		name    string
		filter  dto.SearchFilter
		wantSQL string
		wantArg any
	}{
		{"search", dto.SearchFilter{Key: "name", Value: "jerry", Type: SearchTypeSearch}, "LIKE", "%jerry%"},
		{"exact", dto.SearchFilter{Key: "status", Value: "active", Type: SearchTypeExact}, "IN", "active"},
		{"gt", dto.SearchFilter{Key: "score", Value: 10, Type: OpGT}, ">", 10},
		{"lte", dto.SearchFilter{Key: "score", Value: 99, Type: OpLTE}, "<=", 99},
		{"neq", dto.SearchFilter{Key: "status", Value: "banned", Type: OpNEQ}, "<>", "banned"},
		{"default eq", dto.SearchFilter{Key: "age", Value: 20, Type: ""}, "=", 20},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSelector()
			ApplyFilters([]dto.SearchFilter{tc.filter}, s)
			query, args := render(t, s)
			if !strings.Contains(query, tc.wantSQL) {
				t.Fatalf("query %q missing %q", query, tc.wantSQL)
			}
			if len(args) != 1 || args[0] != tc.wantArg {
				t.Fatalf("args = %v, want [%v]", args, tc.wantArg)
			}
		})
	}
}

func TestApplyFiltersNullChecks(t *testing.T) {
	s := newSelector()
	ApplyFilters([]dto.SearchFilter{{Key: "deleted_at", Type: OpIsNull}}, s)
	query, _ := render(t, s)
	if !strings.Contains(query, "IS NULL") {
		t.Fatalf("query %q missing IS NULL", query)
	}
}

func TestApplyFiltersInList(t *testing.T) {
	s := newSelector()
	ApplyFilters([]dto.SearchFilter{{Key: "id", Value: []any{1, 2, 3}, Type: OpIn}}, s)
	query, args := render(t, s)
	if !strings.Contains(query, "IN") || len(args) != 3 {
		t.Fatalf("query %q args %v, want IN with 3 args", query, args)
	}
}

func TestApplyFiltersRejectsInjection(t *testing.T) {
	injections := []string{
		"name`; DROP TABLE users; --",
		"name = '' OR 1=1",
		"users.name",
	}
	for _, key := range injections {
		s := newSelector()
		ApplyFilters([]dto.SearchFilter{{Key: key, Value: "x", Type: OpEQ}}, s)
		query, _ := render(t, s)
		if strings.Contains(query, "WHERE") {
			t.Fatalf("injection key %q produced a predicate: %q", key, query)
		}
	}
}

func TestApplyFiltersWhitelist(t *testing.T) {
	s := newSelector()
	ApplyFilters([]dto.SearchFilter{
		{Key: "name", Value: "jerry", Type: OpEQ},
		{Key: "password_hash", Value: "x", Type: OpEQ},
	}, s, "name", "status")

	query, args := render(t, s)
	if !strings.Contains(query, "name") || strings.Contains(query, "password_hash") {
		t.Fatalf("whitelist not enforced: %q", query)
	}
	if len(args) != 1 {
		t.Fatalf("args = %v, want only the whitelisted filter", args)
	}
}

func TestApplyFiltersSnakeCasesKeys(t *testing.T) {
	s := newSelector()
	ApplyFilters([]dto.SearchFilter{{Key: "createdBy", Value: "u1", Type: OpEQ}}, s)
	query, _ := render(t, s)
	if !strings.Contains(query, "created_by") {
		t.Fatalf("camelCase key was not snake_cased: %q", query)
	}
}

func TestApplySortDefaultsToIDDesc(t *testing.T) {
	s := newSelector()
	ApplySort(nil, s)
	query, _ := render(t, s)
	if !strings.Contains(query, "ORDER BY") || !strings.Contains(query, "id") || !strings.Contains(query, "DESC") {
		t.Fatalf("missing default id DESC order: %q", query)
	}
}

func TestApplySortUsesGivenColumns(t *testing.T) {
	s := newSelector()
	ApplySort([]dto.SortOption{{Key: "score", Order: -1}, {Key: "name", Order: 1}}, s)
	query, _ := render(t, s)
	if !strings.Contains(query, "score") || !strings.Contains(query, "name") {
		t.Fatalf("sort columns missing: %q", query)
	}
}

func TestApplySortRejectsInvalidColumn(t *testing.T) {
	s := newSelector()
	ApplySort([]dto.SortOption{{Key: "name; DROP TABLE users", Order: 1}}, s)
	query, _ := render(t, s)
	if strings.Contains(query, "DROP") {
		t.Fatalf("injection reached ORDER BY: %q", query)
	}
	// Falls back to the stable default instead.
	if !strings.Contains(query, "ORDER BY") {
		t.Fatalf("missing fallback order: %q", query)
	}
}

func TestApplyPaginationCursorSkipsOffset(t *testing.T) {
	s := newSelector()
	ApplyPagination(&dto.PaginationOptions{PageSize: 20, Cursor: "0197fda1-aaaa"}, s)
	query, args := render(t, s)

	if strings.Contains(query, "OFFSET") {
		t.Fatalf("cursor pagination must not use OFFSET: %q", query)
	}
	if !strings.Contains(query, "<") || len(args) != 1 || args[0] != "0197fda1-aaaa" {
		t.Fatalf("missing keyset predicate: %q args %v", query, args)
	}
	if !strings.Contains(query, "LIMIT 20") {
		t.Fatalf("missing limit: %q", query)
	}
}

func TestApplyPaginationOffsetWithoutCursor(t *testing.T) {
	s := newSelector()
	ApplyPagination(&dto.PaginationOptions{Page: 3, PageSize: 10}, s)
	query, _ := render(t, s)
	if !strings.Contains(query, "OFFSET 20") {
		t.Fatalf("missing offset: %q", query)
	}
}

func TestApplyQueryOptionsFull(t *testing.T) {
	s := newSelector()
	ApplyQueryOptions(&dto.QueryOptions{
		Filters:    []dto.SearchFilter{{Key: "status", Value: "active", Type: OpEQ}},
		Sort:       []dto.SortOption{{Key: "created_at", Order: -1}},
		Pagination: &dto.PaginationOptions{Page: 3, PageSize: 20},
	}, s, "status", "created_at")

	query, args := render(t, s)
	for _, want := range []string{"WHERE", "ORDER BY", "LIMIT 20", "OFFSET 40"} {
		if !strings.Contains(query, want) {
			t.Fatalf("query %q missing %q", query, want)
		}
	}
	if len(args) != 1 || args[0] != "active" {
		t.Fatalf("args = %v", args)
	}
}
