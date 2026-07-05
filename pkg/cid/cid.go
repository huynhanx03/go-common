// Package cid provides a correlation ID that travels with a request across
// services, goroutines, and message queues, tying together every log line
// and hop the request produces. One ID, end to end.
package cid

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

const Header = "X-Correlation-ID"

type ctxKey struct{}

// New returns a new time-ordered correlation ID (UUIDv7) — its prefix encodes
// the creation time, so IDs sort chronologically.
func New() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString() // v4 fallback; NewV7 fails only without entropy
	}
	return id.String()
}

// WithContext returns a context carrying the correlation ID.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the correlation ID, or "" when the context has none.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// EnsureContext returns ctx unchanged when it already carries a correlation
// ID, otherwise a child context with a fresh one. Use at non-HTTP entry
// points (cron jobs, consumers, startup tasks) so downstream logs correlate.
func EnsureContext(ctx context.Context) context.Context {
	if FromContext(ctx) != "" {
		return ctx
	}
	return WithContext(ctx, New())
}

// RoundTripper wraps next so every outgoing HTTP request carries the
// context's correlation ID header. A header already set by the caller wins.
// Pass nil to wrap http.DefaultTransport.
func RoundTripper(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return roundTripper{next: next}
}

type roundTripper struct {
	next http.RoundTripper
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if id := FromContext(req.Context()); id != "" && req.Header.Get(Header) == "" {
		req = req.Clone(req.Context()) // RoundTrippers must not mutate the caller's request
		req.Header.Set(Header, id)
	}
	return rt.next.RoundTrip(req)
}
