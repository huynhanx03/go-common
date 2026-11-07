package middlewares

import (
	"errors"

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
//
// Deprecated: inbound global circuit breakers couple unrelated requests.
// Apply a circuit breaker to one named outbound dependency instead.
func CircuitBreakerMiddleware(cb *algorithm.CircuitBreaker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cb == nil {
			internalServerError(c)
			return
		}
		err := cb.Execute(func() (bool, error) {
			c.Next()
			return c.Writer.Status() < cbServerErrorThreshold, nil
		})
		if errors.Is(err, algorithm.ErrCircuitOpen) {
			response.ErrorResponse(c, apperr.CodeServiceUnavailable, apperr.New(
				apperr.CodeServiceUnavailable,
				"service temporarily unavailable",
				err,
			))
			c.Abort()
			return
		}
	}
}
