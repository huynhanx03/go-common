package middlewares

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/correlation"
)

// Correlation validates or mints a request correlation ID, stores it in the
// request context, and returns it to the caller in the response header.
func Correlation() gin.HandlerFunc {
	return func(c *gin.Context) {
		ensureCorrelation(c)
		c.Next()
	}
}

func ensureCorrelation(c *gin.Context) context.Context {
	ctx := c.Request.Context()
	id := c.GetHeader(correlation.Header)
	if correlation.Validate(id) != nil {
		id = correlation.FromContext(ctx)
	}
	if id == "" {
		id = correlation.New()
	}

	ctx = correlation.WithContext(ctx, id)
	c.Request = c.Request.WithContext(ctx)
	c.Header(correlation.Header, id)
	return ctx
}
