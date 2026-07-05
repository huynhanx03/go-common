package response

import (
	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

type Data struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type WithPagination struct {
	Code       int         `json:"code"`
	Message    string      `json:"message"`
	Data       any         `json:"data"`
	Pagination *Pagination `json:"pagination,omitempty"`
}

// SuccessResponse sends a successful response.
func SuccessResponse(c *gin.Context, code int, data any) {
	c.JSON(GetHTTPCode(code), Data{
		Code:    code,
		Message: Msg[code],
		Data:    data,
	})
}

// ErrorResponse sends an error response. Accepts *apperr.AppError or error.
// For AppError: uses Code + Message from the error, derives HTTP status from Code.
// For plain error: uses the provided fallback code with default message.
func ErrorResponse(c *gin.Context, code int, err any) {
	msgStr := Msg[code]
	httpCode := GetHTTPCode(code)

	if e, ok := err.(*apperr.AppError); ok && e != nil {
		code = e.Code
		httpCode = GetHTTPCode(e.Code)
		msgStr = e.Message
	}

	c.JSON(httpCode, Data{
		Code:    code,
		Message: msgStr,
		Data:    nil,
	})
}
