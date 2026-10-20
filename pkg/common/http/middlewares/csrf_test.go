package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

type csrfVerifierFunc func(context.Context, *http.Request) error

func (fn csrfVerifierFunc) Verify(ctx context.Context, request *http.Request) error {
	return fn(ctx, request)
}

func TestCSRFOnlyChecksUnsafeMethods(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	middleware, err := CSRF(csrfVerifierFunc(func(context.Context, *http.Request) error {
		calls.Add(1)
		return nil
	}), http.MethodGet, http.MethodHead, http.MethodOptions)
	if err != nil {
		t.Fatalf("CSRF: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.Any("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPatch} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, "/", nil))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d", method, recorder.Code)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("CSRF verifier calls = %d, want 2 unsafe methods", got)
	}
}

func TestCSRFFailureIsRedacted(t *testing.T) {
	t.Parallel()

	middleware, err := CSRF(csrfVerifierFunc(func(context.Context, *http.Request) error {
		return errors.New("csrf-token=top-secret")
	}))
	if err != nil {
		t.Fatalf("CSRF: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", nil))
	if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), "top-secret") {
		t.Fatalf("CSRF failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if _, err := CSRF(nil); err == nil {
		t.Fatal("CSRF accepted nil verifier")
	}
}
