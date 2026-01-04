package dto

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
}

// Paginated contains paginated data with pagination info
type Paginated[T any] struct {
	Records    *[]T            `json:"records"`
	Pagination *PaginationMeta `json:"pagination"`
}

// SetDefaults sets default values for pagination
func (p *PaginationOptions) SetDefaults() {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 10
	}
	// if p.PageSize > 100 {
	// 	p.PageSize = 100
	// }
}

// CalculatePagination calculates pagination information
func CalculatePagination(currentPage, pageSize int, totalItems int64) *PaginationMeta {
	totalPages := int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	currentPage = min(currentPage, totalPages)

	if totalPages == 0 {
		totalPages = 1
	}

	return &PaginationMeta{
		CurrentPage: currentPage,
		PageSize:    pageSize,
		TotalPages:  totalPages,
		TotalItems:  totalItems,
		HasNext:     currentPage < totalPages,
		HasPrev:     currentPage > 1,
	}
}
