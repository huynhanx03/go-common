package logger

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRequestAndCorrelationIDsRoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "envoy-request_42:part.1")
	if got := RequestID(ctx); got != "envoy-request_42:part.1" {
		t.Fatalf("RequestID() = %q", got)
	}
	if got := CorrelationID(ctx); got != "envoy-request_42:part.1" {
		t.Fatalf("CorrelationID() = %q", got)
	}
	ctx = WithCorrelationID(ctx, "job-9")
	if got := RequestID(ctx); got != "envoy-request_42:part.1" {
		t.Fatalf("WithCorrelationID changed request ID to %q", got)
	}
	if got := CorrelationID(ctx); got != "job-9" {
		t.Fatalf("CorrelationID() = %q, want job-9", got)
	}
}

func TestRequestIDHelpersIgnoreInvalidAndNilValues(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("a", maxRequestIDBytes+1), "contains space", "é"} {
		ctx := WithRequestID(nil, id)
		if ctx == nil || RequestID(ctx) != "" || CorrelationID(ctx) != "" {
			t.Errorf("WithRequestID(nil, %q) did not preserve an empty safe context", id)
		}
		ctx = WithCorrelationID(nil, id)
		if ctx == nil || CorrelationID(ctx) != "" {
			t.Errorf("WithCorrelationID(nil, %q) did not preserve an empty safe context", id)
		}
	}
	if RequestID(nil) != "" || CorrelationID(nil) != "" {
		t.Fatal("nil context returned an ID")
	}
}

func TestValidateRequestIDBoundaries(t *testing.T) {
	if err := ValidateRequestID(strings.Repeat("a", maxRequestIDBytes)); err != nil {
		t.Fatalf("maximum-length ID rejected: %v", err)
	}
	for _, id := range []string{"", strings.Repeat("a", maxRequestIDBytes+1), "bad/id", "bad\nvalue"} {
		if err := ValidateRequestID(id); !errors.Is(err, ErrInvalidRequestID) {
			t.Errorf("ValidateRequestID(%q) error = %v, want ErrInvalidRequestID", id, err)
		}
	}
}
