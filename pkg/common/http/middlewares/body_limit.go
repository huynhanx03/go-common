package middlewares

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

// DefaultBodyLimit caps request bodies at 1 MiB — plenty for JSON APIs.
const DefaultBodyLimit = 1 << 20

// BodyLimit rejects request bodies larger than maxBytes (<= 0 uses
// DefaultBodyLimit). Without it a single oversized request can exhaust
// memory, since JSON binding reads the whole body. When the limit is hit,
// ParseRequest returns CodeBodyTooLarge → HTTP 413.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = DefaultBodyLimit
	}
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			response.ErrorResponse(c, apperr.CodeBodyTooLarge, apperr.New(
				apperr.CodeBodyTooLarge,
				"request body too large",
				nil,
			))
			c.Abort()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
