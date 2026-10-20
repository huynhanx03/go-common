package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHandlerLivenessAndReadiness(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t, Options{})
	secretFailure := errors.New("postgres://admin:secret@database")
	if err := registry.Register("event-loop", checkerFunc(func(context.Context) error { return nil }), CheckOptions{
		Probes: ProbeLiveness, Critical: true, Timeout: time.Second, ErrorCode: "event_loop_stalled",
	}); err != nil {
		t.Fatalf("Register live: %v", err)
	}
	if err := registry.Register("database", checkerFunc(func(context.Context) error { return secretFailure }), CheckOptions{
		Probes: ProbeReadiness, Critical: true, Timeout: time.Second, ErrorCode: "database_unavailable",
	}); err != nil {
		t.Fatalf("Register ready: %v", err)
	}

	handler := HTTPHandler(registry)
	for _, tc := range []struct {
		path       string
		wantStatus int
		healthy    bool
	}{
		{path: "/live", wantStatus: http.StatusOK, healthy: true},
		{path: "/ready", wantStatus: http.StatusServiceUnavailable, healthy: false},
		{path: "/unknown", wantStatus: http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != tc.wantStatus {
			t.Fatalf("%s status = %d, want %d", tc.path, recorder.Code, tc.wantStatus)
		}
		if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "postgres://") {
			t.Fatalf("%s response exposed raw error: %s", tc.path, recorder.Body.String())
		}
		if tc.path == "/unknown" {
			continue
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("%s content type = %q", tc.path, got)
		}
		var report Report
		if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
			t.Fatalf("%s response JSON: %v", tc.path, err)
		}
		if report.Healthy != tc.healthy {
			t.Fatalf("%s healthy = %v, want %v", tc.path, report.Healthy, tc.healthy)
		}
	}
}

func TestHTTPHandlerRejectsMethodAndHandlesNilRegistry(t *testing.T) {
	t.Parallel()

	handler := HTTPHandler(nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil registry status = %d, want 503", recorder.Code)
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "nil") {
		t.Fatalf("nil implementation detail leaked: %s", recorder.Body.String())
	}

	registry := newTestRegistry(t, Options{})
	recorder = httptest.NewRecorder()
	HTTPHandler(registry).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/live", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /live status = %d, want 405", recorder.Code)
	}
}
