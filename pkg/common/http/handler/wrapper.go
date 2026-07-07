package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/request"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

// HandlerFunc is the generic function signature
type HandlerFunc[T any, R any] func(context.Context, *T) (R, error)

// Wrap converts a generic handler to a Gin handler: it parses and validates
// T, calls h, and renders the result. Returning *response.Reply overrides the
// default 200/CodeSuccess (see response.Created, response.Paginated).
//
// Every error is attached to the Gin context so the request logger records
// its full cause chain — the client only ever sees code + message + cid.
func Wrap[T any, R any](h HandlerFunc[T, R]) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, err := request.ParseRequest[T](c)
		if err != nil {
			fail(c, apperr.CodeParamInvalid, err)
			return
		}

		res, err := h(c.Request.Context(), req)
		if err != nil {
			fail(c, apperr.CodeInternalServer, err)
			return
		}

		response.Respond(c, res)
	}
}

// WrapNoReq is Wrap for endpoints with no input at all (bare GETs like
// health or stats): no DTO, no binding, no validation.
func WrapNoReq[R any](h func(context.Context) (R, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, err := h(c.Request.Context())
		if err != nil {
			fail(c, apperr.CodeInternalServer, err)
			return
		}

		response.Respond(c, res)
	}
}

// fail records err for the request logger and renders the client response.
func fail(c *gin.Context, fallbackCode int, err error) {
	_ = c.Error(err)
	response.ErrorResponse(c, fallbackCode, err)
}
