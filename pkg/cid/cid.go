// Package cid is the backwards-compatible facade for pkg/correlation.
//
// Deprecated: use github.com/huynhanx03/go-common/pkg/correlation.
package cid

import (
	"context"
	"net/http"

	httpRequest "github.com/huynhanx03/go-common/pkg/common/http/request"
	"github.com/huynhanx03/go-common/pkg/correlation"
)

const Header = correlation.Header

// New returns a new time-ordered correlation ID.
//
// Deprecated: use correlation.New.
func New() string {
	return correlation.New()
}

// WithContext returns a context carrying the correlation ID.
//
// Deprecated: use correlation.WithContext.
func WithContext(ctx context.Context, id string) context.Context {
	return correlation.WithContext(ctx, id)
}

// FromContext returns the correlation ID, or "" when the context has none.
//
// Deprecated: use correlation.FromContext.
func FromContext(ctx context.Context) string {
	return correlation.FromContext(ctx)
}

// EnsureContext returns ctx unchanged when it already carries a correlation
// ID, otherwise a child context with a fresh one. Use at non-HTTP entry
// points (cron jobs, consumers, startup tasks) so downstream logs correlate.
//
// Deprecated: use correlation.EnsureContext.
func EnsureContext(ctx context.Context) context.Context {
	return correlation.EnsureContext(ctx)
}

// RoundTripper wraps next so every outgoing HTTP request carries the
// context's correlation ID header. A header already set by the caller wins.
// Pass nil to wrap http.DefaultTransport.
//
// Deprecated: use request.CorrelationRoundTripper.
func RoundTripper(next http.RoundTripper) http.RoundTripper {
	return httpRequest.CorrelationRoundTripper(next)
}
