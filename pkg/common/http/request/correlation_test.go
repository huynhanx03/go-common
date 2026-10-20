package request

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/correlation"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCorrelationRoundTripper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		contextID     string
		headerID      string
		want          string
		wantUnchanged string
	}{
		{name: "context injected", contextID: "context-id", want: "context-id"},
		{name: "valid caller wins", contextID: "context-id", headerID: "caller-id", want: "caller-id", wantUnchanged: "caller-id"},
		{name: "invalid caller replaced", contextID: "context-id", headerID: "invalid id", want: "context-id", wantUnchanged: "invalid id"},
		{name: "invalid caller removed without context", headerID: "invalid id", want: "", wantUnchanged: "invalid id"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var received *http.Request
			transport := CorrelationRoundTripper(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				received = req
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}))

			ctx := context.Background()
			if tc.contextID != "" {
				ctx = correlation.WithContext(ctx, tc.contextID)
			}
			original, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test", nil)
			if err != nil {
				t.Fatalf("NewRequestWithContext: %v", err)
			}
			if tc.headerID != "" {
				original.Header.Set(correlation.Header, tc.headerID)
			}

			if _, err := transport.RoundTrip(original); err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			if got := received.Header.Get(correlation.Header); got != tc.want {
				t.Fatalf("transport header = %q, want %q", got, tc.want)
			}
			if got := original.Header.Get(correlation.Header); got != tc.wantUnchanged {
				t.Fatalf("original request mutated: header = %q, want %q", got, tc.wantUnchanged)
			}
			if received != original {
				received.Header.Set("X-Test-Mutation", "true")
				if original.Header.Get("X-Test-Mutation") != "" {
					t.Fatal("cloned request shares the caller's header map")
				}
			}
		})
	}
}

func TestCorrelationRoundTripperDelegatesNilRequest(t *testing.T) {
	t.Parallel()

	called := false
	transport := CorrelationRoundTripper(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		if req != nil {
			t.Fatalf("request = %#v, want nil", req)
		}
		return nil, context.Canceled
	}))
	response, err := transport.RoundTrip(nil)
	if response != nil || err != context.Canceled || !called {
		t.Fatalf("response=%#v error=%v called=%t", response, err, called)
	}

	if CorrelationRoundTripper(nil) == nil {
		t.Fatal("nil next transport did not resolve to a transport")
	}
}
