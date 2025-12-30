package request

import (
	"github.com/gin-gonic/gin"
	
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/common/http/validation"
)

func ParseRequest[T any](c *gin.Context) (*T, bool) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorResponse(c, response.CodeParamInvalid, response.ToErrorResponse(err))
		return nil, false
	}

	if ok, msg := validation.IsRequestValid(req); !ok {
		response.ErrorResponse(c, response.CodeValidationFailed, response.ToErrorResponse(msg))
		return nil, false
	}

	return &req, true
}
