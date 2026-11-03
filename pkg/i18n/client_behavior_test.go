package i18n

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

type emptyL2 struct{}

func (emptyL2) Get(context.Context, string) ([]byte, bool, error)       { return nil, false, nil }
func (emptyL2) SnapshotVersion(context.Context, string) (string, error) { return "", nil }
func (emptyL2) SetWithVersion(context.Context, string, string, []byte, time.Duration, string) error {
	return nil
}
func (emptyL2) DeleteValueWithVersion(context.Context, string, string, string) error { return nil }
func (emptyL2) Revoke(context.Context, string) error                                 { return nil }
func (emptyL2) RevokeMany(context.Context, ...string) error                          { return nil }
func (emptyL2) Close()                                                               {}

type revokeFailureL1 struct {
	cache.L1Engine
	err error
}

func (l1 *revokeFailureL1) Revoke(context.Context, string) error { return l1.err }

func newCoverageCacheWithL1(t testing.TB, l1 cache.L1Engine) *cache.Cache {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggerConfig{Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := cache.New(cache.Config{L1: l1, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(shared.Close)
	return shared
}

func TestClientConstructionValidatesDependenciesAndPolicy(t *testing.T) {
	ctx := context.Background()
	validSource := SourceFunc(func(context.Context, string) (Catalog, error) { return Catalog{}, nil })
	shared := newCoverageCache(t)
	if _, err := New(nil, settings.I18n{Domain: "billing"}, ClientConfig{Source: validSource, Cache: shared}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil context error=%v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := New(canceled, settings.I18n{Domain: "billing"}, ClientConfig{Source: validSource, Cache: shared}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error=%v", err)
	}
	var nilSource SourceFunc
	for name, config := range map[string]settings.I18n{
		"invalid domain":   {Domain: "bad domain"},
		"invalid ttl":      {Domain: "billing", CacheTTLSeconds: -1},
		"ttl overflow":     {Domain: "billing", CacheTTLSeconds: math.MaxInt},
		"invalid timeout":  {Domain: "billing", SourceTimeoutSeconds: -1},
		"timeout overflow": {Domain: "billing", SourceTimeoutSeconds: math.MaxInt},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(ctx, config, ClientConfig{Source: validSource, Cache: shared}); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("invalid runtime policy accepted: %v", err)
			}
		})
	}
	for name, dependencies := range map[string]ClientConfig{
		"nil source":       {Cache: shared},
		"typed nil source": {Source: nilSource, Cache: shared},
		"nil cache":        {Source: validSource},
		"cache without L1": {Source: validSource, Cache: l2OnlyCache(t)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(ctx, settings.I18n{Domain: "billing"}, dependencies); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("invalid client dependencies accepted: %v", err)
			}
		})
	}
	client, err := New(ctx, settings.I18n{Domain: "billing"}, ClientConfig{Source: validSource, Cache: shared})
	if err != nil {
		t.Fatal(err)
	}
	if client.ttl != time.Duration(DefaultCacheTTLSeconds)*time.Second || client.sourceTimeout != time.Duration(DefaultSourceTimeoutSeconds)*time.Second {
		t.Fatalf("default runtime policy ttl=%s source timeout=%s", client.ttl, client.sourceTimeout)
	}
	_ = client.Close()
}

func l2OnlyCache(t testing.TB) *cache.Cache {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggerConfig{Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := cache.New(cache.Config{L2: emptyL2{}, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(shared.Close)
	return shared
}

func TestSecondsOrDefaultBoundsTimeValues(t *testing.T) {
	if got, err := secondsOrDefault(0, 5); err != nil || got != 5*time.Second {
		t.Fatalf("default seconds=%s err=%v", got, err)
	}
	if got, err := secondsOrDefault(3, 5); err != nil || got != 3*time.Second {
		t.Fatalf("configured seconds=%s err=%v", got, err)
	}
	for _, value := range []int{-1, math.MaxInt} {
		if _, err := secondsOrDefault(value, 5); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("invalid seconds %d accepted: %v", value, err)
		}
	}
}

func TestClientInvalidationAdvancesLocalEpochAndReportsCacheFailures(t *testing.T) {
	ctx := context.Background()
	inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
	if err != nil {
		t.Fatal(err)
	}
	shared := newCoverageCacheWithL1(t, inner)
	client, err := New(ctx, settings.I18n{Domain: "billing"}, ClientConfig{
		Source: SourceFunc(func(context.Context, string) (Catalog, error) { return Catalog{}, nil }), Cache: shared,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.index.Store(&localeIndex{})
	if err := client.invalidate(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil invalidation context: %v", err)
	}
	if err := client.invalidate(ctx); err != nil || client.epoch.Load() != 1 || client.index.Load() != nil {
		t.Fatalf("successful invalidation epoch=%d index=%v err=%v", client.epoch.Load(), client.index.Load(), err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.invalidate(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed client invalidation: %v", err)
	}

	failure := errors.New("cache backend failed")
	inner2, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
	if err != nil {
		t.Fatal(err)
	}
	shared = newCoverageCacheWithL1(t, &revokeFailureL1{L1Engine: inner2, err: failure})
	client, err = New(ctx, settings.I18n{Domain: "billing"}, ClientConfig{
		Source: SourceFunc(func(context.Context, string) (Catalog, error) { return Catalog{}, nil }), Cache: shared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.invalidate(ctx); !errors.Is(err, failure) || client.epoch.Load() != 1 {
		t.Fatalf("failed invalidation was hidden or not fenced: epoch=%d err=%v", client.epoch.Load(), err)
	}
	_ = client.Close()
}

var _ cache.L2Engine = emptyL2{}
