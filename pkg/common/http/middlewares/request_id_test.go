package middlewares

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/logger"
)

func TestCorrelationMiddlewareValidatesInboundHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name   string
		header string
		reuse  bool
	}{
		{name: "valid", header: "upstream-42", reuse: true},
		{name: "invalid", header: "invalid id", reuse: false},
		{name: "missing", reuse: false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(RequestID())
			router.GET("/test", func(c *gin.Context) {
				id := logger.RequestID(c.Request.Context())
				if id != "" && logger.ValidateRequestID(id) != nil {
					t.Fatalf("handler request ID is invalid: %q", id)
				}
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tc.header != "" {
				req.Header.Set(logger.RequestIDHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			got := rec.Header().Get(logger.RequestIDHeader)
			if tc.reuse && got != tc.header {
				t.Fatalf("response correlation ID = %q, want %q", got, tc.header)
			}
			if !tc.reuse && got != "" {
				t.Fatalf("response request ID = %q, want none", got)
			}
		})
	}
}

func TestRequestLoggerUsesRouteTemplateAndRedactsSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.DebugLevel)
	router := gin.New()
	router.Use(RequestLogger(logger.Wrap(zap.New(core))))
	router.GET("/users/:id", func(c *gin.Context) {
		_ = c.Error(errors.New("password=hunter2 token=top-secret"))
		c.Status(http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42?page=1&access_token=top-secret", nil)
	req.Header.Set(logger.RequestIDHeader, "trace-request")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if logs.Len() != 1 {
		t.Fatalf("log entries = %d, want 1", logs.Len())
	}
	entry := logs.All()[0]
	fields := entry.ContextMap()
	if fields["path"] != "/users/:id" {
		t.Fatalf("logged path = %v, want route template", fields["path"])
	}
	if fields["request_id"] != "trace-request" {
		t.Fatalf("logged request_id = %v, want trace-request", fields["request_id"])
	}
	query, _ := fields["query"].(string)
	if strings.Contains(query, "top-secret") || !strings.Contains(query, redactedValue) {
		t.Fatalf("query was not safely redacted: %q", query)
	}
	if _, exists := fields["errors"]; exists {
		t.Fatal("raw handler errors were logged")
	}
	if fields["error_count"] != int64(1) {
		t.Fatalf("error_count = %v, want 1", fields["error_count"])
	}
	for key, value := range fields {
		if strings.Contains(key, "top-secret") || strings.Contains(toString(value), "top-secret") || strings.Contains(toString(value), "hunter2") {
			t.Fatalf("secret leaked through field %q: %v", key, value)
		}
	}
}

func TestRequestLoggerKeepsExpectedClientFailuresOutOfWarningLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.DebugLevel)
	router := gin.New()
	router.Use(RequestLogger(logger.Wrap(zap.New(core))))
	router.GET("/bad-request", func(c *gin.Context) {
		c.Status(http.StatusBadRequest)
	})
	router.GET("/rate-limited", func(c *gin.Context) {
		c.Status(http.StatusTooManyRequests)
	})

	for _, path := range []string{"/bad-request", "/rate-limited"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	}
	entries := logs.FilterMessage("request").All()
	if len(entries) != 2 {
		t.Fatalf("request entries = %d, want 2", len(entries))
	}
	if entries[0].Level != zap.InfoLevel {
		t.Fatalf("400 level = %s, want info", entries[0].Level)
	}
	if entries[1].Level != zap.WarnLevel {
		t.Fatalf("429 level = %s, want warn", entries[1].Level)
	}
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
