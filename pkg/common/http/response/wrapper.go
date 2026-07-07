package response

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/cid"
	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/dto"
)

// Body is the response envelope every endpoint returns.
type Body struct {
	Code       int                 `json:"code"`
	Message    string              `json:"message"`
	Data       any                 `json:"data"`
	Pagination *dto.PaginationMeta `json:"pagination,omitempty"`
	CID        string              `json:"cid,omitempty"`
}

// SuccessResponse sends a successful response.
func SuccessResponse(c *gin.Context, code int, data any) {
	c.JSON(GetHTTPCode(code), Body{
		Code:    code,
		Message: Msg[code],
		Data:    data,
	})
}

// Respond renders a handler result: a *Reply is sent with its own code and
// pagination, anything else is plain data under CodeSuccess.
func Respond(c *gin.Context, res any) {
	if r, ok := res.(*Reply); ok && r != nil {
		c.JSON(GetHTTPCode(r.code), Body{
			Code:       r.code,
			Message:    Msg[r.code],
			Data:       r.data,
			Pagination: r.pagination,
		})
		return
	}
	SuccessResponse(c, apperr.CodeSuccess, res)
}

// ErrorResponse sends an error response. When err carries an *apperr.AppError
// anywhere in its chain, that error's code, message, and details win;
// otherwise the fallback code with its default message is used. The body
// includes the request's correlation ID (cid), so a client-side error report
// can be matched to server logs directly — the same field name logs use.
//
// The error itself is never rendered — technical detail stays in logs
// (attach it via c.Error(err) so the request logger picks it up).
func ErrorResponse(c *gin.Context, code int, err error) {
	msg := Msg[code]
	var details any

	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		code = appErr.Code
		details = appErr.Details
		if msg = appErr.Message; msg == "" {
			msg = Msg[code]
		}
	}

	c.JSON(GetHTTPCode(code), Body{
		Code:    code,
		Message: msg,
		Data:    details,
		CID:     cid.FromContext(c.Request.Context()),
	})
}
