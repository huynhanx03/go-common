package dto

import (
	"errors"
	"math"
)

const (
	DefaultPageSize = 10
	MaxPageSize     = 100
)

var ErrInvalidPagination = errors.New("dto: invalid pagination")

// SearchFilter represents search and filter parameters
type SearchFilter struct {
	Key   string `json:"key" form:"key"`     // Field name to search/filter
	Value any    `json:"value" form:"value"` // Value to search/filter
	Type  string `json:"type" form:"type"`   // "search" or "filter" or "exact"
}

// SortOption represents sorting parameters
type SortOption struct {
	Key   string `json:"key" form:"key"`     // Field name to sort by
	Order int    `json:"order" form:"order"` // 1 for ascending, -1 for descending
}

// PaginationOptions represents pagination parameters
type PaginationOptions struct {
	Page     int `json:"page" form:"page" binding:"min=1"`
	PageSize int `json:"page_size" form:"page_size" binding:"min=1,max=100"`
	Cursor   any `json:"cursor" form:"cursor"` // Cursor for keyset pagination (optional)
}

// QueryOptions combines pagination, search/filter, and sorting
type QueryOptions struct {
	Pagination *PaginationOptions `json:"pagination"`
	Filters    []SearchFilter     `json:"filters"`
	Sort       []SortOption       `json:"sort"`
}

// PaginationMeta contains pagination information
type PaginationMeta struct {
	CurrentPage int   `json:"current_page"`
	PageSize    int   `json:"page_size"`
	TotalPages  int   `json:"total_pages"`
	TotalItems  int64 `json:"total_items"`
	HasNext     bool  `json:"has_next"`
	HasPrev     bool  `json:"has_prev"`
	NextCursor  any   `json:"next_cursor,omitempty"`
}

// Paginated contains paginated data with pagination info
type Paginated[T any] struct {
	Records    *[]T            `json:"records"`
	Pagination *PaginationMeta `json:"pagination"`
}

// SetDefaults sets default values for pagination
func (p *PaginationOptions) SetDefaults() {
	if p == nil {
		return
	}
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
}

// Validate checks positive bounded values and offset arithmetic.
func (p PaginationOptions) Validate() error {
	if p.Page <= 0 ||
		p.PageSize <= 0 ||
		p.PageSize > MaxPageSize ||
		p.Page-1 > math.MaxInt/p.PageSize {
		return ErrInvalidPagination
	}
	return nil
}

// Offset returns the checked zero-based row offset.
func (p PaginationOptions) Offset() (int, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	return (p.Page - 1) * p.PageSize, nil
}

// CalculatePaginationChecked calculates validated pagination metadata.
func CalculatePaginationChecked(
	currentPage,
	pageSize int,
	totalItems int64,
) (*PaginationMeta, error) {
	options := PaginationOptions{Page: currentPage, PageSize: pageSize}
	if err := options.Validate(); err != nil || totalItems < 0 {
		return nil, ErrInvalidPagination
	}
	totalPages64 := totalItems / int64(pageSize)
	if totalItems%int64(pageSize) != 0 {
		totalPages64++
	}
	if totalPages64 == 0 {
		totalPages64 = 1
	}
	if totalPages64 > int64(math.MaxInt) {
		return nil, ErrInvalidPagination
	}
	totalPages := int(totalPages64)
	if currentPage > totalPages {
		currentPage = totalPages
	}
	return &PaginationMeta{
		CurrentPage: currentPage,
		PageSize:    pageSize,
		TotalPages:  totalPages,
		TotalItems:  totalItems,
		HasNext:     currentPage < totalPages,
		HasPrev:     currentPage > 1,
	}, nil
}

// CalculatePagination calculates pagination information.
// Deprecated: use CalculatePaginationChecked for untrusted values.
func CalculatePagination(currentPage, pageSize int, totalItems int64) *PaginationMeta {
	meta, err := CalculatePaginationChecked(currentPage, pageSize, totalItems)
	if err == nil {
		return meta
	}
	return &PaginationMeta{
		CurrentPage: 1,
		PageSize:    DefaultPageSize,
		TotalPages:  1,
	}
}
