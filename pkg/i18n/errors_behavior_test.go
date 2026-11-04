package i18n

import (
	"errors"
	"strings"
	"testing"
)

func TestPublicFailuresPreserveCategoriesWithoutExposingBackendDetails(t *testing.T) {
	secret := errors.New("database password must stay private")
	wrapped := sourceError(secret)
	if !errors.Is(wrapped, ErrSource) || !errors.Is(wrapped, secret) {
		t.Fatalf("source failure lost its classification or cause: %v", wrapped)
	}
	if strings.Contains(wrapped.Error(), secret.Error()) {
		t.Fatalf("source failure exposed backend detail: %v", wrapped)
	}

	committed := &CommittedError{Revision: 12, cause: secret}
	if !errors.Is(committed, ErrInvalidation) || !errors.Is(committed, secret) {
		t.Fatalf("committed failure lost retry information: %v", committed)
	}
	if strings.Contains(committed.Error(), secret.Error()) {
		t.Fatalf("committed failure exposed cache detail: %v", committed)
	}
}
