package middlewares

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/logger"
)

// redactedValue replaces sensitive query parameter values in access logs.
// Alphanumeric so url.Values.Encode leaves it readable.
const redactedValue = "REDACTED"

// RequestLogger logs each HTTP request with the injected service logger.
// Envoy supplies X-Request-ID; the middleware carries it into request logs.
// Pass the application's configured logger.
func RequestLogger(base *logger.Logger) gin.HandlerFunc {
	base = base.Named("http")

	return func(c *gin.Context) {
		start := time.Now()

		propagateRequestID(c)

		c.Next()

		// Log after request completes
		latency := time.Since(start)
		status := c.Writer.Status()

		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.String("ip", c.ClientIP()),
		}

		if query := c.Request.URL.RawQuery; query != "" {
			fields = append(fields, zap.String("query", redactQuery(query)))
		}

		if userID, ok := c.Request.Context().Value(constraints.ContextKeyUserID).(string); ok && userID != "" {
			fields = append(fields, zap.String("user_id", userID))
		}

		// Errors attached by handlers via c.Error(err) — without this, a 500
		// in the access log says nothing about its cause.
		if len(c.Errors) > 0 {
			fields = append(fields, zap.Int("error_count", len(c.Errors)))
		}

		if status >= 500 {
			base.Error(c.Request.Context(), "request", fields...)
		} else if status == http.StatusTooManyRequests {
			base.Warn(c.Request.Context(), "request", fields...)
		} else {
			base.Info(c.Request.Context(), "request", fields...)
		}
	}
}

// redactQuery masks values of sensitive query parameters so credentials
// never land in log storage. Non-sensitive parameters pass through unchanged.
func redactQuery(rawQuery string) string {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return redactedValue // unparsable — safer to hide entirely
	}

	changed := false
	for k := range values {
		if isSensitiveParam(k) {
			values[k] = []string{redactedValue}
			changed = true
		}
	}
	if !changed {
		return rawQuery
	}
	return values.Encode()
}

// sensitiveMarkers flags any query parameter whose name contains one of these
// substrings — catches variants like access_token, refresh_token, client_secret.
var sensitiveMarkers = []string{
	"token",
	"password",
	"secret",
	"credential",
	"auth",
	"ticket",
	"payload",
	"source",
	"body",
	"content",
}

// sensitiveKeys flags query parameters by exact name (lowercase).
var sensitiveKeys = map[string]struct{}{
	"key":       {},
	"api_key":   {},
	"apikey":    {},
	"code":      {},
	"sig":       {},
	"signature": {},
	"otp":       {},
}

// isSensitiveParam reports whether a query parameter is likely to carry a credential.
func isSensitiveParam(key string) bool {
	key = strings.ToLower(key)

	if _, ok := sensitiveKeys[key]; ok {
		return true
	}
	for _, marker := range sensitiveMarkers {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}
