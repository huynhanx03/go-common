package middlewares

import (
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
			// Check if the panic is an error or other type
			var appErr error
			if e, ok := err.(error); ok {
				appErr = e
			} else {
				appErr = fmt.Errorf("%v", err)
			}

			// Log the stack trace
			logger.FromContext(c.Request.Context()).Error("panic recovered",
				zap.Error(appErr),
				zap.String("stack", string(debug.Stack())),
			)

			// Return standardized error response
			response.ErrorResponse(c, response.CodeInternalServer, apperr.New(
				response.CodeInternalServer,
				"Internal Server Error",
				appErr,
			))
			// Ensure we abort the context to stop propagation
			c.Abort()
		}
	}()
	c.Next()
}
