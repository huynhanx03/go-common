package middlewares

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBodyLimitRejectsKnownOversizeBodyBeforeHandler(t *testing.T) {
	t.Parallel()

	called := false
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(BodyLimit(4))
	router.POST("/", func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345")))
	if recorder.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("status=%d handler_called=%v, want 413 and no handler", recorder.Code, called)
	}
}
