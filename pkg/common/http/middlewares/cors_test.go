package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCORSExactOriginsAndVary(t *testing.T) {
	t.Parallel()

	middleware, err := CORS(CORSOptions{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowedMethods:   []string{http.MethodGet, http.MethodPost},
		AllowedHeaders:   []string{"Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"X-Correlation-ID"},
		AllowCredentials: true,
		MaxAge:           time.Hour,
	})
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.Any("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, tc := range []struct {
		name        string
		origin      string
		wantAllowed bool
	}{
		{name: "exact", origin: "https://app.example.com", wantAllowed: true},
		{name: "suffix attack", origin: "https://app.example.com.attacker.test"},
		{name: "wrong port", origin: "https://app.example.com:444"},
		{name: "wrong scheme", origin: "http://app.example.com"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Origin", tc.origin)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			allowed := recorder.Header().Get("Access-Control-Allow-Origin")
			if tc.wantAllowed && allowed != tc.origin {
				t.Fatalf("allow origin = %q, want %q", allowed, tc.origin)
			}
			if !tc.wantAllowed && allowed != "" {
				t.Fatalf("disallowed origin received %q", allowed)
			}
			if vary := recorder.Header().Values("Vary"); len(vary) == 0 {
				t.Fatal("response does not vary on Origin")
			}
		})
	}
}

func TestCORSPreflightAndValidation(t *testing.T) {
	t.Parallel()

	if _, err := CORS(CORSOptions{
		AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, AllowCredentials: true,
	}); err == nil {
		t.Fatal("CORS accepted wildcard with credentials")
	}
	if _, err := CORS(CORSOptions{
		AllowedOrigins: []string{"https://example.com/path"}, AllowedMethods: []string{"GET"},
	}); err == nil {
		t.Fatal("CORS accepted an origin with a path")
	}

	middleware, err := CORS(CORSOptions{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{http.MethodPost},
		AllowedHeaders: []string{"X-CSRF-Token"},
		MaxAge:         time.Minute,
	})
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.Any("/", func(c *gin.Context) { c.Status(http.StatusTeapot) })

	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "X-CSRF-Token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Methods") != http.MethodPost {
		t.Fatalf("allow methods = %q", recorder.Header().Get("Access-Control-Allow-Methods"))
	}
}
