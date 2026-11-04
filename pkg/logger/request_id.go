package logger

import (
	"context"
	"fmt"
)

type requestIDContextKey struct{}
type correlationIDContextKey struct{}

// WithRequestID stores a validated request ID in ctx. Invalid values are ignored.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if ValidateRequestID(id) != nil {
		return ctx
	}
	ctx = context.WithValue(ctx, requestIDContextKey{}, id)
	return WithCorrelationID(ctx, id)
}

// RequestID returns the request ID carried by ctx, or an empty string.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	if ValidateRequestID(id) != nil {
		return ""
	}
	return id
}

// WithCorrelationID stores an internal correlation ID in ctx. For requests
// entering through Envoy, use the same value as RequestID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if ValidateRequestID(id) != nil {
		return ctx
	}
	return context.WithValue(ctx, correlationIDContextKey{}, id)
}

// CorrelationID returns the internal correlation ID carried by ctx, or empty.
func CorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(correlationIDContextKey{}).(string)
	if ValidateRequestID(id) != nil {
		return ""
	}
	return id
}

// ValidateRequestID accepts bounded, log-safe IDs from trusted transports.
func ValidateRequestID(id string) error {
	if len(id) == 0 {
		return fmt.Errorf("%w: empty", ErrInvalidRequestID)
	}
	if len(id) > maxRequestIDBytes {
		return fmt.Errorf("%w: exceeds %d bytes", ErrInvalidRequestID, maxRequestIDBytes)
	}
	for index := 0; index < len(id); index++ {
		char := id[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '.', '_', ':', '-':
		default:
			return fmt.Errorf("%w: unsupported character at byte %d", ErrInvalidRequestID, index)
		}
	}
	return nil
}
