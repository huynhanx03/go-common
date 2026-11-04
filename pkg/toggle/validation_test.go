package toggle_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

func TestPublicIdentifierValidationEnforcesCallerCategoryAndByteLimit(t *testing.T) {
	category := errors.New("caller validation category")
	if err := toggle.Identifier("checkout-v2", core.MaxIdentifierLength, category); err != nil {
		t.Fatalf("valid identifier error = %v", err)
	}
	if err := toggle.Identifier("bad identifier", core.MaxIdentifierLength, category); !errors.Is(err, category) {
		t.Fatalf("invalid identifier error = %v", err)
	}
	if err := toggle.Identifier(strings.Repeat("a", core.MaxIdentifierLength+1), core.MaxIdentifierLength, category); !errors.Is(err, category) || !errors.Is(err, core.ErrLimit) {
		t.Fatalf("oversized identifier error = %v", err)
	}
}
