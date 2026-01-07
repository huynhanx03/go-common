package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/http/request"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

// HandlerFunc is the generic function signature
type HandlerFunc[T any, R any] func(context.Context, *T) (R, error)

// Wrap converts a generic handler to a Gin handler
func Wrap[T any, R any](h HandlerFunc[T, R]) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, err := request.ParseRequest[T](c)
		if err != nil {
			response.ErrorResponse(c, response.CodeParamInvalid, err)
			return
		}

		res, err := h(c.Request.Context(), req)
		if err != nil {
			response.ErrorResponse(c, response.CodeInternalServer, err)
			return
		}

		response.SuccessResponse(c, response.CodeSuccess, res)
	}
}
