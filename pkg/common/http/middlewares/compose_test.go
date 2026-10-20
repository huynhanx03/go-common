package middlewares

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestComposeRunsOnceInDeclaredOrder(t *testing.T) {
	t.Parallel()

	var order []string
	middleware := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) {
			order = append(order, name+":before")
			c.Next()
			order = append(order, name+":after")
		}
	}
	final := func(c *gin.Context) {
		order = append(order, "handler")
		c.Status(http.StatusNoContent)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", Compose(middleware("one"), middleware("two"))(final)...)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	want := "one:before,two:before,handler,two:after,one:after"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}
}
