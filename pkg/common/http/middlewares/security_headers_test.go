package middlewares

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	middleware, err := SecurityHeaders(SecurityHeadersOptions{HSTSMaxAge: time.Hour})
	if err != nil {
		t.Fatalf("SecurityHeaders: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	request.TLS = &tls.ConnectionState{}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	for key, want := range map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=3600",
	} {
		if got := recorder.Header().Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if recorder.Header().Get("Content-Security-Policy") == "" ||
		recorder.Header().Get("Permissions-Policy") == "" {
		t.Fatal("default security policy headers are missing")
	}

	if _, err := SecurityHeaders(SecurityHeadersOptions{ContentSecurityPolicy: "default-src 'none'\r\nX-Evil: true"}); err == nil {
		t.Fatal("SecurityHeaders accepted header injection")
	}
}
