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

func TestComposeHandlersCopiesDeclaredChain(t *testing.T) {
	t.Parallel()

	var order []string
	first := func(c *gin.Context) { order = append(order, "first"); c.Next() }
	second := func(c *gin.Context) { order = append(order, "second"); c.Next() }
	chain := []gin.HandlerFunc{first, second}
	composed := ComposeHandlers(chain...)
	chain[0] = func(c *gin.Context) { order = append(order, "replaced") }
	composed = append(composed, func(c *gin.Context) { order = append(order, "handler") })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", composed...)
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if got := strings.Join(order, ","); got != "first,second,handler" {
		t.Fatalf("composed order = %q", got)
	}
}
