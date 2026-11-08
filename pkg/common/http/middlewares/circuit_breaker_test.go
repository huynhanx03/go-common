package middlewares

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/algorithm"
)

func TestCircuitBreakerMiddlewareLimitsHalfOpenProbes(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })

	var now atomic.Int64
	breaker := algorithm.NewCircuitBreaker(
		algorithm.WithFailureThreshold(1),
		algorithm.WithSuccessThreshold(1),
		algorithm.WithOpenTimeout(time.Second),
		algorithm.WithBreakerClock(now.Load),
	)
	router := gin.New()
	router.Use(CircuitBreakerMiddleware(breaker))

	probeStarted := make(chan struct{})
	finishProbe := make(chan struct{})
	var calls atomic.Int32
	router.GET("/dependency", func(c *gin.Context) {
		switch calls.Add(1) {
		case 1:
			c.Status(http.StatusInternalServerError)
		case 2:
			close(probeStarted)
			<-finishProbe
			c.Status(http.StatusOK)
		default:
			c.Status(http.StatusOK)
		}
	})
	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dependency", nil))
		return response
	}

	if response := request(); response.Code != http.StatusInternalServerError {
		t.Fatalf("initial response status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	now.Add(int64(time.Second))

	probeResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() { probeResponse <- request() }()
	<-probeStarted

	if response := request(); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("request during half-open probe status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("dependency calls while probe is running = %d, want 2", got)
	}

	close(finishProbe)
	if response := <-probeResponse; response.Code != http.StatusOK {
		t.Fatalf("probe response status = %d, want %d", response.Code, http.StatusOK)
	}
	if response := request(); response.Code != http.StatusOK {
		t.Fatalf("response after successful probe = %d, want %d", response.Code, http.StatusOK)
	}
}
