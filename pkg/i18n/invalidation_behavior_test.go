package i18n

import (
	"context"
	"errors"
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
)

func TestInvalidateRetriesOnlyValidCommittedCatalogRevisions(t *testing.T) {
	manager := newCoverageManager(t, newCoverageStore())
	if err := manager.Invalidate(context.Background(), "billing", 1); err != nil {
		t.Fatalf("valid explicit invalidation error=%v", err)
	}
	var nilManager *Manager
	if err := nilManager.Invalidate(context.Background(), "billing", 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil manager invalidation error=%v", err)
	}
	if err := manager.Invalidate(nil, "billing", 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil invalidation context error=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Invalidate(canceled, "billing", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled invalidation error=%v", err)
	}
	for _, test := range []struct {
		domain   string
		revision int64
	}{
		{domain: "bad domain", revision: 1},
		{domain: "billing", revision: 0},
		{domain: "billing", revision: -1},
	} {
		if err := manager.Invalidate(context.Background(), test.domain, test.revision); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("invalid invalidation target domain=%q revision=%d error=%v", test.domain, test.revision, err)
		}
	}

	inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("cache backend failed")
	manager, err = NewManager(newCoverageStore(), newCoverageCacheWithL1(t, &revokeFailureL1{L1Engine: inner, err: failure}))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Invalidate(context.Background(), "billing", 1); !errors.Is(err, failure) {
		t.Fatalf("cache invalidation failure was hidden: %v", err)
	}
}
