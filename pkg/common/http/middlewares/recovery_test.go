package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRecoveryWritesAtMostOneRedactedResponse(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RecoveryMiddleware)
	router.GET("/before-write", func(*gin.Context) { panic("password=top-secret") })
	router.GET("/after-write", func(c *gin.Context) {
		c.String(http.StatusOK, "already-written")
		panic("password=top-secret")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/before-write", nil))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "top-secret") {
		t.Fatalf("pre-write panic status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/after-write", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "already-written" {
		t.Fatalf("committed response was overwritten: status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "top-secret") {
			t.Fatal("panic secret leaked in log message")
		}
		for _, value := range entry.ContextMap() {
			if text, ok := value.(string); ok && strings.Contains(text, "top-secret") {
				t.Fatal("panic secret leaked in structured log")
			}
		}
	}
}

func TestRecoveryPreservesCancellation(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RecoveryMiddleware)
	router.GET("/", func(*gin.Context) { panic(context.Canceled) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Body.Len() != 0 {
		t.Fatalf("cancellation rendered an error body: %s", recorder.Body.String())
	}
	if logs.Len() != 0 {
		t.Fatalf("cancellation was logged as panic: %+v", logs.All())
	}
}

func TestRecoveryLogsNonCancellationPanicAfterRequestCancellation(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RecoveryMiddleware)
	router.GET("/", func(*gin.Context) { panic("credential=do-not-log") })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Body.Len() != 0 {
		t.Fatalf("cancelled request rendered an error body: %s", recorder.Body.String())
	}
	if logs.FilterMessage("panic recovered").Len() != 1 {
		t.Fatalf("non-cancellation panic was not logged exactly once: %+v", logs.All())
	}
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "do-not-log") {
			t.Fatalf("panic value leaked in message: %+v", entry)
		}
		for _, value := range entry.ContextMap() {
			if text, ok := value.(string); ok && strings.Contains(text, "do-not-log") {
				t.Fatalf("panic value leaked in structured log: %+v", entry)
			}
		}
	}
}
