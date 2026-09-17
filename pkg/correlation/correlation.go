// Package correlation owns the canonical correlation ID context value shared
// by HTTP, gRPC, WebSocket, message, job, and worker boundaries.
package correlation

import (
	"context"

	"github.com/google/uuid"
)

// Header is the canonical HTTP and metadata header for correlation IDs.
const Header = "X-Correlation-ID"

type contextKey struct{}

// New returns a time-ordered UUIDv7 correlation ID.
func New() string {
	id, err := uuid.NewV7()
	if err != nil {
		// UUIDv7 can only fail when system entropy is unavailable. UUIDv4 keeps
		// the API total while preserving the same validated wire format.
		return uuid.NewString()
	}
	return id.String()
}

// WithContext returns a context carrying id. Invalid identifiers are ignored
// so an untrusted value cannot poison downstream logs or transport metadata.
func WithContext(ctx context.Context, id string) context.Context {
	ctx = nonNilContext(ctx)
	if Validate(id) != nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext returns the valid correlation ID stored in ctx, or an empty
// string when none exists.
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(contextKey{}).(string)
	if Validate(id) != nil {
		return ""
	}
	return id
}

// EnsureContext returns ctx unchanged when it already carries a valid
// correlation ID, otherwise it returns a child context with a fresh ID.
func EnsureContext(ctx context.Context) context.Context {
	ctx = nonNilContext(ctx)
	if FromContext(ctx) != "" {
		return ctx
	}
	return WithContext(ctx, New())
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
