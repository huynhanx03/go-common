package middlewares

import (
	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/algorithm"
	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

// Circuit breaker HTTP thresholds.
const (
	cbServerErrorThreshold = 500 // HTTP status >= 500 counts as failure
)

// CircuitBreakerMiddleware wraps a route group with a circuit breaker.
// When the downstream error rate exceeds the threshold, the circuit opens
// and immediately returns 503 Service Unavailable without calling the handler.
func CircuitBreakerMiddleware(cb *algorithm.CircuitBreaker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cb.Allow(); err != nil {
			response.ErrorResponse(c, response.CodeInternalServer, apperr.New(
				response.CodeInternalServer,
				"service temporarily unavailable",
				err,
			))
			c.Abort()
			return
		}

		c.Next()

		if c.Writer.Status() >= cbServerErrorThreshold {
			cb.RecordFailure()
		} else {
			cb.RecordSuccess()
		}
	}
}
