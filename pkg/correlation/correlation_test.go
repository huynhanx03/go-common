package correlation

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := []string{
		"a",
		"abc-123",
		"edge.api:v1_request-42",
		strings.Repeat("a", 128),
	}
	for _, id := range valid {
		id := id
		t.Run("valid_"+id[:1], func(t *testing.T) {
			t.Parallel()
			if err := Validate(id); err != nil {
				t.Fatalf("Validate(%q) = %v, want nil", id, err)
			}
		})
	}

	invalid := []string{
		"",
		" ",
		"has space",
		"has\tcontrol",
		"has\nnewline",
		"unicode-é",
		"slash/not-allowed",
		strings.Repeat("a", 129),
	}
	for _, id := range invalid {
		id := id
		t.Run("invalid", func(t *testing.T) {
			t.Parallel()
			if err := Validate(id); err == nil {
				t.Fatalf("Validate(%q) = nil, want error", id)
			}
		})
	}
}

func TestNewReturnsUUIDv7(t *testing.T) {
	t.Parallel()

	id, err := uuid.Parse(New())
	if err != nil {
		t.Fatalf("New() is not a UUID: %v", err)
	}
	if id.Version() != 7 {
		t.Fatalf("New() UUID version = %d, want 7", id.Version())
	}
}

func TestContextRoundTripAndEnsure(t *testing.T) {
	t.Parallel()

	if got := FromContext(nil); got != "" {
		t.Fatalf("FromContext(nil) = %q, want empty", got)
	}

	ctx := WithContext(nil, "request-42")
	if got := FromContext(ctx); got != "request-42" {
		t.Fatalf("FromContext = %q, want request-42", got)
	}
	if ensured := EnsureContext(ctx); ensured != ctx {
		t.Fatal("EnsureContext replaced a valid existing context")
	}

	generated := EnsureContext(context.Background())
	if err := Validate(FromContext(generated)); err != nil {
		t.Fatalf("EnsureContext generated invalid ID: %v", err)
	}
}

func TestWithContextRejectsInvalidID(t *testing.T) {
	t.Parallel()

	ctx := WithContext(context.Background(), "contains whitespace")
	if got := FromContext(ctx); got != "" {
		t.Fatalf("invalid correlation ID was stored: %q", got)
	}
}
