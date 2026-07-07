package response

import (
	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/dto"
)

// Reply lets a handler override the default 200/CodeSuccess rendering by
// returning it as the result value — the route stays a plain handler.Wrap:
//
//	return response.Created(user), nil          // 201, code 20001
//	return response.Updated(user), nil          // 200, code 20002
//	return response.Deleted(nil), nil           // 200, code 20003
//	return response.Paginated(items, meta), nil // 200 + pagination block
//	return response.With(code, data), nil       // any other code
//
// Plain GETs need none of this — returning the data itself renders 200/20000.
type Reply struct {
	code       int
	data       any
	pagination *dto.PaginationMeta
}

// Created marks the result of a resource creation (HTTP 201).
func Created(data any) *Reply {
	return &Reply{code: apperr.CodeCreated, data: data}
}

// Updated marks the result of a resource update (HTTP 200, code 20002).
func Updated(data any) *Reply {
	return &Reply{code: apperr.CodeUpdated, data: data}
}

// Deleted marks the result of a resource deletion (HTTP 200, code 20003).
// Pass nil, or something like gin.H{"id": id} to echo what was removed.
func Deleted(data any) *Reply {
	return &Reply{code: apperr.CodeDeleted, data: data}
}

// Paginated attaches pagination metadata (from dto.CalculatePagination) to a
// list result.
func Paginated(data any, meta *dto.PaginationMeta) *Reply {
	return &Reply{code: apperr.CodeSuccess, data: data, pagination: meta}
}

// With renders data under an arbitrary success code.
func With(code int, data any) *Reply {
	return &Reply{code: code, data: data}
}
