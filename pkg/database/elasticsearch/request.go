package elasticsearch

import (
	"github.com/huynhanx03/go-common/pkg/dto"
)

// BuildSearchQuery constructs the complete Elasticsearch body
func BuildSearchQuery(opts *dto.QueryOptions) map[string]any {
	if opts == nil {
		opts = &dto.QueryOptions{}
	}

	body := make(map[string]any)

	// Pagination
	pagination := BuildPagination(opts.Pagination)
	for k, v := range pagination {
		body[k] = v
	}

	// Filters
	query := BuildFilter(&opts.Filters)
	if len(query) > 0 {
		body["query"] = query
	} else {
		body["query"] = map[string]any{
			"match_all": map[string]any{},
		}
	}

	// Sorting
	sort := BuildSort(&opts.Sort)
	if len(sort) > 0 {
		body["sort"] = sort
	}

	return body
}

// BuildPagination creates ES pagination fields
func BuildPagination(p *dto.PaginationOptions) map[string]any {
	if p == nil {
		p = &dto.PaginationOptions{}
	}
	p.SetDefaults()

	result := map[string]any{
		"size": p.PageSize,
	}

	// Use search_after if cursor is present (Keyset Pagination)
	if p.Cursor != nil && p.Cursor != "" {
		// If cursor is a slice (multi-field sort), use it directly
		if cursorSlice, ok := p.Cursor.([]any); ok {
			result["search_after"] = cursorSlice
			return result
		}
		// If cursor is a single value (e.g. ID), wrap it in a slice
		// This assumes the query is sorted by a single field (like _id)
		result["search_after"] = []any{p.Cursor}
		return result
	}

	// Fallback to Offset Pagination
	result["from"] = (p.Page - 1) * p.PageSize
	return result
}

// BuildFilter creates ES boolean query from filters
func BuildFilter(filters *[]dto.SearchFilter) map[string]any {
	if filters == nil || len(*filters) == 0 {
		return nil
	}

	var must []map[string]any
	var filter []map[string]any

	for _, f := range *filters {
		switch f.Type {
		case "match":
			must = append(must, map[string]any{
				"match": map[string]any{
					f.Key: f.Value,
				},
			})
		case "term":
			filter = append(filter, map[string]any{
				"term": map[string]any{
					f.Key: f.Value,
				},
			})
		case "phrase":
			must = append(must, map[string]any{
				"match_phrase": map[string]any{
					f.Key: f.Value,
				},
			})
		case "wildcard":
			filter = append(filter, map[string]any{
				"wildcard": map[string]any{
					f.Key: f.Value,
				},
			})
		}
	}

	boolQuery := map[string]any{}
	if len(must) > 0 {
		boolQuery["must"] = must
	}
	if len(filter) > 0 {
		boolQuery["filter"] = filter
	}

	if len(boolQuery) == 0 {
		return nil
	}

	return map[string]any{
		"bool": boolQuery,
	}
}

// BuildSort creates ES sort list
func BuildSort(sorts *[]dto.SortOption) []map[string]any {
	if sorts == nil {
		return nil
	}

	var esSort []map[string]any

	for _, s := range *sorts {
		order := "asc"
		if s.Order == -1 {
			order = "desc"
		}
		esSort = append(esSort, map[string]any{
			s.Key: map[string]any{
				"order": order,
			},
		})
	}
	return esSort
}
