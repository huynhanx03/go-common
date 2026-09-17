package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHandlerExposesDeterministicPrometheusText(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(Options{MaxMetrics: 3, MaxSeries: 4})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	gauge, err := registry.Register(Descriptor{
		Name: "z_queue_depth", Help: "Queued work.", Kind: GaugeKind, MaxSeries: 1,
	})
	if err != nil {
		t.Fatalf("register gauge: %v", err)
	}
	counter, err := registry.Register(Descriptor{
		Name: "a_requests_total", Help: "Requests\\processed\nby outcome.", Kind: CounterKind,
		Labels: []LabelSpec{{Name: "outcome", Values: []string{"ok"}}}, MaxSeries: 1,
	})
	if err != nil {
		t.Fatalf("register counter: %v", err)
	}
	histogram, err := registry.Register(Descriptor{
		Name: "m_latency_seconds", Help: "Latency.", Kind: HistogramKind,
		Buckets: []float64{0.5, 1}, MaxSeries: 1,
	})
	if err != nil {
		t.Fatalf("register histogram: %v", err)
	}
	ctx := context.Background()
	if err := gauge.Set(ctx, 2, nil); err != nil {
		t.Fatalf("set gauge: %v", err)
	}
	if err := counter.Add(ctx, 3, Labels{"outcome": "ok"}); err != nil {
		t.Fatalf("add counter: %v", err)
	}
	if err := histogram.Observe(ctx, 0.75, nil); err != nil {
		t.Fatalf("observe histogram: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	HTTPHandler(registry).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	want := strings.Join([]string{
		`# HELP a_requests_total Requests\\processed\nby outcome.`,
		`# TYPE a_requests_total counter`,
		`a_requests_total{outcome="ok"} 3`,
		`# HELP m_latency_seconds Latency.`,
		`# TYPE m_latency_seconds histogram`,
		`m_latency_seconds_bucket{le="0.5"} 0`,
		`m_latency_seconds_bucket{le="1"} 1`,
		`m_latency_seconds_bucket{le="+Inf"} 1`,
		`m_latency_seconds_sum 0.75`,
		`m_latency_seconds_count 1`,
		`# HELP z_queue_depth Queued work.`,
		`# TYPE z_queue_depth gauge`,
		`z_queue_depth 2`,
		"",
	}, "\n")
	if got := recorder.Body.String(); got != want {
		t.Fatalf("metrics body:\n%s\nwant:\n%s", got, want)
	}
}

func TestHTTPHandlerFailsClosedForUnsupportedMethodOrRegistry(t *testing.T) {
	t.Parallel()

	post := httptest.NewRecorder()
	HTTPHandler(&Registry{}).ServeHTTP(
		post,
		httptest.NewRequest(http.MethodPost, "/metrics", nil),
	)
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", post.Code, http.StatusMethodNotAllowed)
	}
	if got := post.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", got)
	}

	unavailable := httptest.NewRecorder()
	HTTPHandler(nil).ServeHTTP(
		unavailable,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil registry status = %d, want %d", unavailable.Code, http.StatusServiceUnavailable)
	}

	zero := httptest.NewRecorder()
	HTTPHandler(new(Registry)).ServeHTTP(
		zero,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	if zero.Code != http.StatusServiceUnavailable {
		t.Fatalf("zero registry status = %d, want %d", zero.Code, http.StatusServiceUnavailable)
	}
}
