package request

import (
	"net/http"

	"github.com/huynhanx03/go-common/pkg/logger"
)

// RequestIDRoundTripper propagates a validated request ID on outbound
// requests. A valid caller-supplied header wins. Invalid caller input is
// replaced by a valid context value or removed. The original request and its
// headers are never mutated.
func RequestIDRoundTripper(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return requestIDRoundTripper{next: next}
}

type requestIDRoundTripper struct {
	next http.RoundTripper
}

func (transport requestIDRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return transport.next.RoundTrip(nil)
	}

	callerID := req.Header.Get(logger.RequestIDHeader)
	if logger.ValidateRequestID(callerID) == nil {
		return transport.next.RoundTrip(req)
	}

	contextID := logger.RequestID(req.Context())
	if callerID == "" && contextID == "" {
		return transport.next.RoundTrip(req)
	}

	outbound := req.Clone(req.Context())
	outbound.Header.Del(logger.RequestIDHeader)
	if contextID != "" {
		outbound.Header.Set(logger.RequestIDHeader, contextID)
	}
	return transport.next.RoundTrip(outbound)
}
