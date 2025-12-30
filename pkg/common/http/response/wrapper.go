package response

import (
	"strings"

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

// ToErrorResponse converts error to error response
func ToErrorResponse(input any) string {
	var messages []string

	switch v := input.(type) {
	case string:
		messages = []string{v}
	case []string:
		messages = v
	case error:
		messages = []string{v.Error()}
	default:
		messages = []string{"Unknown error"}
	}

	return strings.Join(messages, ", ")
}

// SuccessResponse sends a successful response
func SuccessResponse(c *gin.Context, code int, data any) {
	c.JSON(GetHTTPCode(code), Data{
		Code:    code,
		Message: Msg[code],
		Data:    data,
	})
}

// ErrorResponse sends an error response
func ErrorResponse(c *gin.Context, code int, err any) {
	var httpCode int
	var msgStr string

	// Default behavior
	httpCode = GetHTTPCode(code)
	msgStr = Msg[code]

	if e, ok := err.(*apperr.AppError); ok {
		// Custom AppError
		if e.Code != 0 {
			code = e.Code
		}
		if e.HTTPStatus != 0 {
			httpCode = e.HTTPStatus
		}
		if e.Message != "" {
			msgStr = e.Message
		}
	} else if e, ok := err.(error); ok {
		// Standard error
		// We deliberately keep msgStr as the safe default (Msg[code])
		// to prevent leaking internal system errors to the client.
		// If you want to expose the error string, use AppError.
		_ = e // Mark used
	}

	c.JSON(httpCode, Data{
		Code:    code,
		Message: msgStr,
		Data:    err, // Detailed error info (e.g. validaton structure)
	})
}
