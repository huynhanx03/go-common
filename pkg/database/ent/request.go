package ent

import (
	"github.com/huynhanx03/go-common/pkg/dto"

	"entgo.io/ent/dialect/sql"

	"github.com/huynhanx03/go-common/pkg/utils"
)

const (
	SearchTypeSearch = "search"
	SearchTypeExact  = "exact"
	SearchTypeFilter = "filter"
)

// ApplyQueryOptions applies all standard query options (Filter, Sort, Pagination).
func ApplyQueryOptions(opts *dto.QueryOptions, selector *sql.Selector) {
	if opts == nil {
		return
	}
	ApplyFilters(opts.Filters, selector)
	ApplySort(opts.Sort, selector)
	ApplyPagination(opts.Pagination, selector)
}

// ApplyFilters applies dynamic filters based on dto.SearchFilter options.
func ApplyFilters(filters []dto.SearchFilter, selector *sql.Selector) {
	for _, f := range filters {
		if f.Key == "" || f.Value == nil {
			continue
		}
		key := utils.ToSnakeCase(f.Key)

		switch f.Type {
		case SearchTypeSearch:
			if str, ok := f.Value.(string); ok && str != "" {
				selector.Where(sql.ContainsFold(key, str))
			}
		case SearchTypeExact, SearchTypeFilter:
			selector.Where(sql.EQ(key, f.Value))
		default:
			selector.Where(sql.EQ(key, f.Value))
		}
	}
}

// ApplySort applies sorting logic.
func ApplySort(sorts []dto.SortOption, selector *sql.Selector) {
	for _, sort := range sorts {
		if sort.Key == "" {
			continue
		}
		key := utils.ToSnakeCase(sort.Key)

		if sort.Order == -1 {
			selector.OrderBy(sql.Desc(key))
		} else {
			selector.OrderBy(sql.Asc(key))
		}
	}
}

// ApplyPagination applies pagination logic (Limit/Offset).
func ApplyPagination(pagination *dto.PaginationOptions, selector *sql.Selector) {
	if pagination == nil {
		return
	}
	pagination.SetDefaults()
	selector.Limit(pagination.PageSize)
	selector.Offset((pagination.Page - 1) * pagination.PageSize)
}
