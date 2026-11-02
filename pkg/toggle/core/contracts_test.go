package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateContextRejectsMissingAndCanceledOperations(t *testing.T) {
	if err := ValidateContext(nil); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("nil context error = %v, want ErrInvalidContext", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateContext(ctx); !errors.Is(err, ErrInvalidContext) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v, want invalid context and cancellation", err)
	}
	if err := ValidateContext(context.Background()); err != nil {
		t.Fatalf("live context error = %v", err)
	}
}

func TestErrorClassificationPreservesCauseWithoutExposingIt(t *testing.T) {
	cause := errors.New("database credentials rejected")
	err := SourceError(cause)
	if !errors.Is(err, ErrSource) || !errors.Is(err, cause) {
		t.Fatalf("source error classification lost category or cause: %v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("source error exposed backend details: %v", err)
	}
	if got := Wrap(nil, cause); got != cause {
		t.Fatalf("Wrap(nil, cause) = %v, want original cause", got)
	}
	if got := Wrap(ErrInvalidConfig, nil); !errors.Is(got, ErrInvalidConfig) {
		t.Fatalf("Wrap(category, nil) = %v, want category", got)
	}
}

func TestValidateExpectedRevisionRejectsUnsafeRevisions(t *testing.T) {
	for _, revision := range []int64{-1, int64(^uint64(0) >> 1)} {
		if err := ValidateExpectedRevision(revision); !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("ValidateExpectedRevision(%d) = %v, want ErrInvalidDefinition", revision, err)
		}
	}
	if err := ValidateExpectedRevision(0); err != nil {
		t.Fatalf("zero revision error = %v", err)
	}
}

func TestIdentifierEnforcesByteLimitAndCategory(t *testing.T) {
	cause := ErrInvalidConfig
	if err := Identifier(strings.Repeat("a", 4), 3, cause); !errors.Is(err, cause) || !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized identifier error = %v, want category and limit", err)
	}
	if err := Identifier("a", 3, cause); err != nil {
		t.Fatalf("valid identifier error = %v", err)
	}
}
