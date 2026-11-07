package http

import (
	"context"
	nethttp "net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type poolTransportFunc func(*nethttp.Request) (*nethttp.Response, error)

func (function poolTransportFunc) RoundTrip(request *nethttp.Request) (*nethttp.Response, error) {
	return function(request)
}

func TestHTTPClientPoolZeroRetriesStillMakesOneAttempt(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	pool := &HTTPClientPool{
		client: &nethttp.Client{Transport: poolTransportFunc(func(request *nethttp.Request) (*nethttp.Response, error) {
			calls.Add(1)
			return &nethttp.Response{
				StatusCode: nethttp.StatusServiceUnavailable,
				Header:     make(nethttp.Header),
				Body:       nethttp.NoBody,
				Request:    request,
			}, nil
		})},
	}
	request, _ := nethttp.NewRequest(nethttp.MethodGet, "https://example.test", nil)

	response, err := pool.RequestWithRetry(context.Background(), request, 0)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	if response == nil || response.StatusCode != nethttp.StatusServiceUnavailable {
		t.Fatalf("response = %#v", response)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestHTTPClientPoolDoExposesAttemptMetadata(t *testing.T) {
	t.Parallel()

	pool := &HTTPClientPool{
		client: &nethttp.Client{Transport: poolTransportFunc(func(request *nethttp.Request) (*nethttp.Response, error) {
			return &nethttp.Response{
				StatusCode: nethttp.StatusOK,
				Header:     make(nethttp.Header),
				Body:       nethttp.NoBody,
				Request:    request,
			}, nil
		})},
	}
	request, _ := nethttp.NewRequest(nethttp.MethodPost, "https://example.test", strings.NewReader("payload"))

	response, attempts, err := pool.Do(context.Background(), request, DefaultRetryPolicy())
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if response == nil || len(attempts) != 1 || attempts[0].StatusCode != nethttp.StatusOK {
		t.Fatalf("response=%#v attempts=%+v", response, attempts)
	}
}

func TestHTTPClientPoolCacheLifecycle(t *testing.T) {
	t.Parallel()

	pool := NewHTTPClientPool(nil)
	if _, found := pool.GetFromCache("profile"); found {
		t.Fatal("new client cache returned a value for a missing key")
	}
	pool.SetCache("profile", "first")
	pool.SetCache("profile", "updated")
	if got, found := pool.GetFromCache("profile"); !found || got != "updated" {
		t.Fatalf("cache lookup = (%v, %v), want updated value", got, found)
	}
	pool.SetCache("settings", 3)
	pool.ClearCache()
	for _, key := range []string{"profile", "settings"} {
		if got, found := pool.GetFromCache(key); found {
			t.Errorf("cache retained %q = %v after clear", key, got)
		}
	}
}

func TestHTTPClientPoolDoRejectsNilPool(t *testing.T) {
	t.Parallel()

	var pool *HTTPClientPool
	request, err := nethttp.NewRequest(nethttp.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, attempts, err := pool.Do(context.Background(), request, RetryPolicy{})
	if err == nil || response != nil || attempts != nil {
		t.Fatalf("Do on nil pool = (%v, %v, %v), want invalid request", response, attempts, err)
	}
}
