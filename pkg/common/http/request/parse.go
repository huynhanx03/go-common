package request

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/validation"
)

// ParseRequest binds URI params, query params, and JSON body (in that order,
// later sources override earlier ones) into a single T, then validates it.
// One struct describes the endpoint's full input contract:
//
//	type UpdateUserReq struct {
//		ID   string `uri:"id" validate:"required"`
//		Name string `json:"name" validate:"required,min=2"`
//	}
//
// Note: URI/query bind errors are non-fatal — required checks belong in
// `validate` tags, not `binding` tags, so they are enforced here.
//
// Client-facing messages stay generic; the technical cause is preserved in
// the AppError's RootCause for server logs only.
func ParseRequest[T any](c *gin.Context) (*T, error) {
	var req T

	// Try to bind URI params (optional, ignore error if no tags)
	_ = c.ShouldBindUri(&req)

	// Try to bind query params before JSON. JSON can still override values for POST handlers.
	_ = c.ShouldBindQuery(&req)

	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, apperr.New(apperr.CodeBodyTooLarge, "request body too large", err)
		}
		return nil, apperr.New(apperr.CodeParamInvalid, "invalid request body", err)
	}

	if errs := validation.Validate(req); len(errs) > 0 {
		return nil, apperr.New(apperr.CodeValidationFailed, errs[0].Message, nil).
			WithDetails(map[string]any{"errors": errs})
	}

	return &req, nil
}
