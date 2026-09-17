package middlewares

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/logger"
)

// RecoveryMiddleware captures panics and returns a 500 error
func RecoveryMiddleware(c *gin.Context) {
	defer func() {
		if err := recover(); err != nil {
			if recoveredError, ok := err.(error); ok &&
				(errors.Is(recoveredError, context.Canceled) ||
					errors.Is(recoveredError, context.DeadlineExceeded)) {
				c.Abort()
				return
			}

			stack := debug.Stack()
			if len(stack) > 32<<10 {
				stack = stack[:32<<10]
			}
			logger.FromContext(c.Request.Context()).Error("panic recovered",
				zap.String("panic_type", fmt.Sprintf("%T", err)),
				zap.ByteString("stack", stack),
			)

			// Preserve the cancelled transport outcome after recording a real
			// panic. Writing a new response after the peer has gone cannot help
			// the caller and can obscure the cancellation in access logs.
			if c.Request.Context().Err() != nil {
				c.Abort()
				return
			}
			if !c.Writer.Written() {
				response.ErrorResponse(c, apperr.CodeInternalServer, apperr.New(
					apperr.CodeInternalServer,
					"internal server error",
					nil,
				))
			}
			c.Abort()
		}
	}()
	c.Next()
}
