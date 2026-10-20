package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRequestDeadline(t *testing.T) {
	t.Parallel()

	if _, err := RequestDeadline(0); err == nil {
		t.Fatal("RequestDeadline accepted zero")
	}
	middleware, err := RequestDeadline(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("RequestDeadline: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.GET("/", func(c *gin.Context) {
		<-c.Request.Context().Done()
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline status = %d, want 504", recorder.Code)
	}
}
