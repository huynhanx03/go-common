package correlation_test

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/cid"
	"github.com/huynhanx03/go-common/pkg/correlation"
)

func TestCIDFacadeSharesCanonicalContextKey(t *testing.T) {
	t.Parallel()

	legacy := cid.WithContext(context.Background(), "legacy-to-new")
	if got := correlation.FromContext(legacy); got != "legacy-to-new" {
		t.Fatalf("correlation.FromContext(cid.WithContext(...)) = %q", got)
	}

	canonical := correlation.WithContext(context.Background(), "new-to-legacy")
	if got := cid.FromContext(canonical); got != "new-to-legacy" {
		t.Fatalf("cid.FromContext(correlation.WithContext(...)) = %q", got)
	}
}
