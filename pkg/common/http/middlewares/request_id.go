package middlewares

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/logger"
)

// RequestID validates and propagates Envoy's request ID on the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		propagateRequestID(c)
		c.Next()
	}
}

func propagateRequestID(c *gin.Context) context.Context {
	ctx := c.Request.Context()
	id := c.GetHeader(logger.RequestIDHeader)
	if logger.ValidateRequestID(id) != nil {
		id = logger.RequestID(ctx)
	}
	if id == "" {
		return ctx
	}

	ctx = logger.WithRequestID(ctx, id)
	c.Request = c.Request.WithContext(ctx)
	c.Header(logger.RequestIDHeader, id)
	return ctx
}
