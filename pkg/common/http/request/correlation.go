package request

import (
	"net/http"

	"github.com/huynhanx03/go-common/pkg/correlation"
)

// CorrelationRoundTripper propagates a validated correlation ID on outbound
// requests. A valid caller-supplied header wins. Invalid caller input is
// replaced by a valid context value or removed. The original request and its
// headers are never mutated.
func CorrelationRoundTripper(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return correlationRoundTripper{next: next}
}

type correlationRoundTripper struct {
	next http.RoundTripper
}

func (transport correlationRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return transport.next.RoundTrip(nil)
	}

	callerID := req.Header.Get(correlation.Header)
	if correlation.Validate(callerID) == nil {
		return transport.next.RoundTrip(req)
	}

	contextID := correlation.FromContext(req.Context())
	if callerID == "" && contextID == "" {
		return transport.next.RoundTrip(req)
	}

	outbound := req.Clone(req.Context())
	outbound.Header.Del(correlation.Header)
	if contextID != "" {
		outbound.Header.Set(correlation.Header, contextID)
	}
	return transport.next.RoundTrip(outbound)
}
