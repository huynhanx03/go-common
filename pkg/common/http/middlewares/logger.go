package middlewares

import (
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/cid"
	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/logger"
)

// redactedValue replaces sensitive query parameter values in access logs.
// Alphanumeric so url.Values.Encode leaves it readable.
const redactedValue = "REDACTED"

// RequestLogger logs every HTTP request and injects a per-request logger into context.
// Each request gets a correlation ID (cid) that travels end to end — reused
// from the X-Correlation-ID header when an upstream service already set one.
// Pass the application's root zap logger; it is namespaced under "http".
func RequestLogger(rootLogger *zap.Logger) gin.HandlerFunc {
	base := rootLogger.Named("http")

	return func(c *gin.Context) {
		start := time.Now()

		// Generate or reuse the correlation ID
		id := c.GetHeader(cid.Header)
		if id == "" {
			id = cid.New()
		}
		c.Header(cid.Header, id)

		// Build per-request logger with cid baked in
		reqLogger := base.With(zap.String("cid", id))

		// Inject cid + logger into context — downstream uses
		// logger.FromContext(ctx), and outgoing HTTP/MQ calls pick up the cid
		ctx := cid.WithContext(c.Request.Context(), id)
		ctx = logger.WithContext(ctx, reqLogger)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		// Log after request completes
		latency := time.Since(start)
		status := c.Writer.Status()

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
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
			fields = append(fields, zap.Strings("errors", c.Errors.Errors()))
		}

		if status >= 500 {
			reqLogger.Error("request", fields...)
		} else if status >= 400 {
			reqLogger.Warn("request", fields...)
		} else {
			reqLogger.Info("request", fields...)
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
