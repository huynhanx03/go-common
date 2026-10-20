package stripe

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	stripesdk "github.com/stripe/stripe-go/v85"
)

// capturedRequest records what the stub backend received, so a test can assert
// the adapter built the right request. body holds the raw request body, which is
// how the JSON-bodied V2 endpoints are inspected (form is empty for those).
type capturedRequest struct {
	method         string
	path           string
	form           map[string][]string
	body           []byte
	idempotencyKey string
}

// newTestProvider builds a provider whose Stripe client talks to a stub backend
// that replies with body for every request. The logger is nil; the adapter's log
// helpers are nil-safe.
func newTestProvider(t *testing.T, body string) (*Provider, *capturedRequest) {
	t.Helper()

	captured := &capturedRequest{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.body, _ = io.ReadAll(r.Body)
		// Restore the body so form parsing still works for form-encoded (v1) calls.
		r.Body = io.NopCloser(bytes.NewReader(captured.body))
		_ = r.ParseForm()

		captured.method = r.Method
		captured.path = r.URL.Path
		captured.form = r.Form
		captured.idempotencyKey = r.Header.Get("Idempotency-Key")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	cfg := &stripesdk.BackendConfig{URL: stripesdk.String(srv.URL)}
	backends := &stripesdk.Backends{
		API:         stripesdk.GetBackendWithConfig(stripesdk.APIBackend, cfg),
		Connect:     stripesdk.GetBackendWithConfig(stripesdk.ConnectBackend, cfg),
		Uploads:     stripesdk.GetBackendWithConfig(stripesdk.UploadsBackend, cfg),
		MeterEvents: stripesdk.GetBackendWithConfig(stripesdk.MeterEventsBackend, cfg),
	}

	client := stripesdk.NewClient("sk_test_dummy", stripesdk.WithBackends(backends))

	return newWithClient(client, nil, "whsec_test"), captured
}

func (c *capturedRequest) formValue(key string) string {
	values := c.form[key]
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
