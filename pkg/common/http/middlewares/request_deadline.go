package middlewares

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

const maxRequestDeadline = 10 * time.Minute

// RequestDeadline adds a bounded request context deadline. Handlers must honor
// cancellation; if they return without writing after the deadline, a stable
// 504 envelope is rendered.
func RequestDeadline(timeout time.Duration) (gin.HandlerFunc, error) {
	if timeout <= 0 || timeout > maxRequestDeadline {
		return nil, errors.New("request deadline: invalid timeout")
	}
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		if errors.Is(ctx.Err(), context.DeadlineExceeded) && !c.Writer.Written() {
			response.ErrorResponse(c, apperr.CodeGatewayTimeout, apperr.New(
				apperr.CodeGatewayTimeout,
				"request deadline exceeded",
				nil,
			))
			c.Abort()
		}
	}, nil
}
