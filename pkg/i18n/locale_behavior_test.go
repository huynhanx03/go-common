package i18n

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
	"github.com/huynhanx03/go-common/pkg/settings"
	"golang.org/x/text/language"
)

type sourceBehavior struct {
	info   func(context.Context, string) (CatalogInfo, error)
	locale func(context.Context, string, string, string) (Catalog, error)
}

type reviveExpiredL1 struct {
	cache.L1Engine
	shared  *cache.Cache
	key     string
	value   cachedInfo
	revives atomic.Int32
}

type closeOnRevokeL1 struct {
	cache.L1Engine
	client *Client
}

func (l1 *closeOnRevokeL1) Revoke(ctx context.Context, key string) error {
	l1.client.Close()
	return l1.L1Engine.Revoke(ctx, key)
}

type closeOnBundleReadL1 struct {
	cache.L1Engine
	client *Client
}

func (l1 *closeOnBundleReadL1) Get(ctx context.Context, key string) (any, bool) {
	value, ok := l1.L1Engine.Get(ctx, key)
	if l1.client != nil && strings.HasPrefix(key, bundleKeyPrefix) {
		l1.client.Close()
	}
	return value, ok
}

func (l1 *reviveExpiredL1) Revoke(ctx context.Context, key string) error {
	if err := l1.L1Engine.Revoke(ctx, key); err != nil {
		return err
	}
	if key == l1.key {
		l1.revives.Add(1)
		_, err := cache.Fetch(ctx, l1.shared, key, time.Hour, func(context.Context) (cachedInfo, error) {
			return l1.value, nil
		}, cache.WithoutRefresh())
		return err
	}
	return nil
}

func (s sourceBehavior) Info(ctx context.Context, domain string) (CatalogInfo, error) {
	return s.info(ctx, domain)
}

func (s sourceBehavior) LoadLocale(ctx context.Context, domain, locale, version string) (Catalog, error) {
	return s.locale(ctx, domain, locale, version)
}

func behaviorCatalog(revision int64, text string) (CatalogInfo, Catalog) {
	info := CatalogInfo{
		Domain: "billing", Revision: revision, Version: strings.Repeat("a", 64),
		DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}},
	}
	catalog := Catalog{
		Domain: "billing", Revision: revision,
		DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}},
		Keys:         []Key{{Definition: Definition{Key: "welcome", Source: Forms{Other: text}}, Translations: map[string]Translation{}}},
	}
	return info, catalog
}

func behaviorClient(t testing.TB, source Source, shared *cache.Cache) *Client {
	t.Helper()
	client, err := New(context.Background(), settings.I18n{Domain: "billing"}, ClientConfig{Source: source, Cache: shared})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func sourceFor(info CatalogInfo, catalog Catalog) sourceBehavior {
	return sourceBehavior{
		info:   func(context.Context, string) (CatalogInfo, error) { return info, nil },
		locale: func(context.Context, string, string, string) (Catalog, error) { return catalog, nil },
	}
}

func TestManifestLoadingHandlesSourceCorruptionExpirationAndRevisionRaces(t *testing.T) {
	ctx := context.Background()
	info, catalog := behaviorCatalog(1, "Hello")

	t.Run("source error is categorized", func(t *testing.T) {
		client := behaviorClient(t, sourceBehavior{
			info: func(context.Context, string) (CatalogInfo, error) {
				return CatalogInfo{}, errors.New("database unavailable")
			},
			locale: func(context.Context, string, string, string) (Catalog, error) { return Catalog{}, nil },
		}, newCoverageCache(t))
		if _, err := client.loadIndex(ctx, 0); !errors.Is(err, ErrSource) {
			t.Fatalf("manifest source error=%v", err)
		}
	})

	t.Run("source metadata must belong to requested domain", func(t *testing.T) {
		invalid := info
		invalid.Domain = "other"
		client := behaviorClient(t, sourceFor(invalid, catalog), newCoverageCache(t))
		if _, err := client.loadIndex(ctx, 0); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("invalid manifest metadata error=%v", err)
		}
	})

	t.Run("cached metadata is revalidated", func(t *testing.T) {
		shared := newCoverageCache(t)
		invalid := info
		invalid.Domain = "other"
		if _, err := cache.Fetch(ctx, shared, manifestKeyPrefix+"billing", time.Hour, func(context.Context) (cachedInfo, error) {
			return cachedInfo{Info: invalid, LoadedAt: time.Now().UnixMilli()}, nil
		}, cache.WithoutRefresh()); err != nil {
			t.Fatal(err)
		}
		client := behaviorClient(t, sourceFor(info, catalog), shared)
		if _, err := client.loadIndex(ctx, 0); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("corrupt cached metadata error=%v", err)
		}
	})

	t.Run("expired metadata reloads once", func(t *testing.T) {
		shared := newCoverageCache(t)
		var calls atomic.Int32
		source := sourceBehavior{
			info:   func(context.Context, string) (CatalogInfo, error) { calls.Add(1); return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) { return catalog, nil },
		}
		if _, err := cache.Fetch(ctx, shared, manifestKeyPrefix+"billing", time.Hour, func(context.Context) (cachedInfo, error) {
			return cachedInfo{Info: info, LoadedAt: time.Now().Add(-2 * time.Hour).UnixMilli()}, nil
		}, cache.WithoutRefresh()); err != nil {
			t.Fatal(err)
		}
		client := behaviorClient(t, source, shared)
		client.ttl = time.Second
		if _, err := client.loadIndex(ctx, 0); err != nil || calls.Load() != 1 {
			t.Fatalf("expired manifest reload calls=%d err=%v", calls.Load(), err)
		}
	})

	t.Run("expired cache deletion failure is surfaced", func(t *testing.T) {
		inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("cache unavailable")
		shared := newCoverageCacheWithL1(t, &revokeFailureL1{L1Engine: inner, err: failure})
		var calls atomic.Int32
		source := sourceBehavior{
			info:   func(context.Context, string) (CatalogInfo, error) { calls.Add(1); return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) { return catalog, nil },
		}
		if _, err := cache.Fetch(ctx, shared, manifestKeyPrefix+"billing", time.Hour, func(context.Context) (cachedInfo, error) {
			return cachedInfo{Info: info, LoadedAt: time.Now().Add(-2 * time.Hour).UnixMilli()}, nil
		}, cache.WithoutRefresh()); err != nil {
			t.Fatal(err)
		}
		client := behaviorClient(t, source, shared)
		client.ttl = time.Second
		if _, err := client.loadIndex(ctx, 0); !errors.Is(err, ErrSource) || calls.Load() != 0 {
			t.Fatalf("cache deletion failure calls=%d err=%v", calls.Load(), err)
		}
	})

	t.Run("epoch race discards manifest", func(t *testing.T) {
		var client *Client
		source := sourceBehavior{
			info:   func(context.Context, string) (CatalogInfo, error) { client.epoch.Add(1); return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) { return catalog, nil },
		}
		client = behaviorClient(t, source, newCoverageCache(t))
		if _, err := client.loadIndex(ctx, 0); !errors.Is(err, ErrStaleRevision) {
			t.Fatalf("manifest crossing invalidation epoch error=%v", err)
		}
	})

	t.Run("epoch race reports invalidation failure", func(t *testing.T) {
		inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("cache unavailable")
		var client *Client
		source := sourceBehavior{
			info:   func(context.Context, string) (CatalogInfo, error) { client.epoch.Add(1); return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) { return catalog, nil },
		}
		client = behaviorClient(t, source, newCoverageCacheWithL1(t, &revokeFailureL1{L1Engine: inner, err: failure}))
		if _, err := client.loadIndex(ctx, 0); !errors.Is(err, ErrSource) || !errors.Is(err, failure) {
			t.Fatalf("epoch invalidation error=%v", err)
		}
	})

	t.Run("sub-millisecond freshness exhausts bounded retries", func(t *testing.T) {
		inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		key := manifestKeyPrefix + "billing"
		stale := cachedInfo{Info: info, LoadedAt: time.Now().Add(-2 * time.Hour).UnixMilli()}
		l1 := &reviveExpiredL1{L1Engine: inner, key: key, value: stale}
		shared := newCoverageCacheWithL1(t, l1)
		l1.shared = shared
		if _, err := cache.Fetch(ctx, shared, key, time.Hour, func(context.Context) (cachedInfo, error) {
			return stale, nil
		}, cache.WithoutRefresh()); err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		source := sourceBehavior{
			info:   func(context.Context, string) (CatalogInfo, error) { calls.Add(1); return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) { return catalog, nil },
		}
		client := behaviorClient(t, source, shared)
		client.ttl = time.Second
		if _, err := client.loadIndex(ctx, 0); !errors.Is(err, ErrStaleRevision) || calls.Load() != 0 || l1.revives.Load() != maxLoadAttempts {
			t.Fatalf("expired retries=%d source calls=%d err=%v", l1.revives.Load(), calls.Load(), err)
		}
	})
}

func TestLocaleProjectionAndBundleLoadingRejectStaleOrCorruptPayloads(t *testing.T) {
	ctx := context.Background()
	info, catalog := behaviorCatalog(2, "Hello")
	if _, err := validateProjection(info, "en", catalog); err != nil {
		t.Fatalf("valid locale projection rejected: %v", err)
	}
	wrongRevision := catalog
	wrongRevision.Revision++
	if _, err := validateProjection(info, "en", wrongRevision); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale locale projection error=%v", err)
	}
	wrongLocale := catalog
	wrongLocale.Locales = []string{"fr"}
	if _, err := validateProjection(info, "en", wrongLocale); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("malformed locale projection error=%v", err)
	}

	t.Run("locale source failure", func(t *testing.T) {
		source := sourceBehavior{
			info: func(context.Context, string) (CatalogInfo, error) { return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) {
				return Catalog{}, errors.New("source offline")
			},
		}
		client := behaviorClient(t, source, newCoverageCache(t))
		if _, err := client.loadBundle(ctx, &localeIndex{info: info}, "en"); !errors.Is(err, ErrSource) {
			t.Fatalf("locale source failure error=%v", err)
		}
	})

	t.Run("stale locale projection", func(t *testing.T) {
		stale := catalog
		stale.Revision++
		client := behaviorClient(t, sourceFor(info, stale), newCoverageCache(t))
		if _, err := client.loadBundle(ctx, &localeIndex{info: info}, "en"); !errors.Is(err, ErrStaleRevision) {
			t.Fatalf("stale locale load error=%v", err)
		}
	})

	t.Run("corrupt cached projection", func(t *testing.T) {
		shared := newCoverageCache(t)
		index := &localeIndex{info: info}
		key := localeKeyPrefix + "billing:" + info.Version + ":en"
		bad := catalog
		bad.Locales = []string{"fr"}
		if _, err := cache.Fetch(ctx, shared, key, time.Hour, func(context.Context) (Catalog, error) { return bad, nil }, cache.WithoutRefresh()); err != nil {
			t.Fatal(err)
		}
		client := behaviorClient(t, sourceFor(info, catalog), shared)
		if _, err := client.loadBundle(ctx, index, "en"); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("corrupt cache projection error=%v", err)
		}
	})
}

func TestTranslateRetriesStaleReadsAndHonorsCloseAndInputValidation(t *testing.T) {
	ctx := context.Background()
	info, catalog := behaviorCatalog(1, "Hello")
	var localeCalls atomic.Int32
	source := sourceBehavior{
		info: func(context.Context, string) (CatalogInfo, error) { return info, nil },
		locale: func(context.Context, string, string, string) (Catalog, error) {
			if localeCalls.Add(1) == 1 {
				return Catalog{}, ErrStaleRevision
			}
			return catalog, nil
		},
	}
	client := behaviorClient(t, source, newCoverageCache(t))
	if _, err := client.Translate(nil, "welcome", Options{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil translation context error=%v", err)
	}
	if _, err := client.Translate(ctx, "bad key", Options{}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("invalid message key error=%v", err)
	}
	if _, err := client.Translate(ctx, "welcome", Options{Locale: strings.Repeat("x", MaxLocaleBytes+1)}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("oversized locale preference error=%v", err)
	}
	if _, err := client.Translate(ctx, "welcome", Options{Params: map[string]any{"bad name": "x"}}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("unsafe template parameter error=%v", err)
	}
	if got, err := client.Translate(ctx, "welcome", Options{Locale: "en"}); err != nil || got.Text != "Hello" || localeCalls.Load() != 2 {
		t.Fatalf("stale locale retry result=%+v calls=%d err=%v", got, localeCalls.Load(), err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Translate(ctx, "welcome", Options{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed client request error=%v", err)
	}
}

func TestTranslateHandlesCloseAndInvalidationRaces(t *testing.T) {
	ctx := context.Background()
	info, catalog := behaviorCatalog(1, "Hello")

	t.Run("stale source invalidation closes client before retry", func(t *testing.T) {
		inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		source := sourceBehavior{
			info: func(context.Context, string) (CatalogInfo, error) { return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) {
				calls.Add(1)
				return Catalog{}, ErrStaleRevision
			},
		}
		l1 := &closeOnRevokeL1{L1Engine: inner}
		client := behaviorClient(t, source, newCoverageCacheWithL1(t, l1))
		l1.client = client
		if _, err := client.Translate(ctx, "welcome", Options{}); !errors.Is(err, ErrClosed) || calls.Load() != 1 {
			t.Fatalf("close during retry calls=%d err=%v", calls.Load(), err)
		}
	})

	t.Run("close after bundle load prevents serving result", func(t *testing.T) {
		inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		l1 := &closeOnBundleReadL1{L1Engine: inner}
		shared := newCoverageCacheWithL1(t, l1)
		client := behaviorClient(t, sourceFor(info, catalog), shared)
		index := &localeIndex{
			info: info, epoch: 0, validUntil: time.Now().Add(time.Hour).UnixMilli(),
			matcher: language.NewMatcher([]language.Tag{language.English}),
			locales: []string{"en"}, exact: map[string]bool{"en": true},
		}
		client.index.Store(index)
		compiled, err := compileCatalog("billing", catalog)
		if err != nil {
			t.Fatal(err)
		}
		key := bundleKeyPrefix + "billing:" + info.Version + ":en"
		if _, err := cache.Fetch(ctx, shared, key, time.Hour, func(context.Context) (*compiledCatalog, error) { return compiled, nil }, cache.L1Only(), cache.WithoutRefresh()); err != nil {
			t.Fatal(err)
		}
		l1.client = client
		if _, err := client.Translate(ctx, "welcome", Options{Locale: "en"}); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed client served bundle: %v", err)
		}
	})

	t.Run("cache failure while discarding stale locale is surfaced", func(t *testing.T) {
		inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("cache unavailable")
		source := sourceBehavior{
			info:   func(context.Context, string) (CatalogInfo, error) { return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) { return Catalog{}, ErrStaleRevision },
		}
		client := behaviorClient(t, source, newCoverageCacheWithL1(t, &revokeFailureL1{L1Engine: inner, err: failure}))
		if _, err := client.Translate(ctx, "welcome", Options{}); !errors.Is(err, failure) {
			t.Fatalf("stale locale invalidation failure=%v", err)
		}
	})

	t.Run("source failure reaches caller", func(t *testing.T) {
		source := sourceBehavior{
			info: func(context.Context, string) (CatalogInfo, error) { return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) {
				return Catalog{}, errors.New("source unavailable")
			},
		}
		client := behaviorClient(t, source, newCoverageCache(t))
		if _, err := client.Translate(ctx, "welcome", Options{}); !errors.Is(err, ErrSource) {
			t.Fatalf("source load error=%v", err)
		}
	})

	t.Run("publication during locale load retries with new version", func(t *testing.T) {
		var client *Client
		currentInfo, currentCatalog := info, catalog
		var changed atomic.Bool
		var localeCalls atomic.Int32
		var infoCalls atomic.Int32
		source := sourceBehavior{
			info: func(context.Context, string) (CatalogInfo, error) { infoCalls.Add(1); return currentInfo, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) {
				if changed.CompareAndSwap(false, true) {
					client.epoch.Add(1)
					currentInfo, currentCatalog = behaviorCatalog(2, "Hello after publish")
					currentInfo.Version = strings.Repeat("b", 64)
					localeCalls.Add(1)
					return catalog, nil
				}
				localeCalls.Add(1)
				return currentCatalog, nil
			},
		}
		client = behaviorClient(t, source, newCoverageCache(t))
		got, err := client.Translate(ctx, "welcome", Options{Locale: "en"})
		if err != nil || got.Text != "Hello after publish" || got.Revision != 2 {
			t.Fatalf("publication race result=%+v info calls=%d locale calls=%d epoch=%d err=%v", got, infoCalls.Load(), localeCalls.Load(), client.epoch.Load(), err)
		}
	})

	t.Run("publication race invalidation failure is surfaced", func(t *testing.T) {
		inner, err := ristretto.New[string, any](ristretto.WithNumCounters(1000), ristretto.WithMaxCost(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("cache unavailable")
		var client *Client
		var changed atomic.Bool
		source := sourceBehavior{
			info: func(context.Context, string) (CatalogInfo, error) { return info, nil },
			locale: func(context.Context, string, string, string) (Catalog, error) {
				if changed.CompareAndSwap(false, true) {
					client.epoch.Add(1)
				}
				return catalog, nil
			},
		}
		client = behaviorClient(t, source, newCoverageCacheWithL1(t, &revokeFailureL1{L1Engine: inner, err: failure}))
		if _, err := client.Translate(ctx, "welcome", Options{}); !errors.Is(err, ErrSource) || !errors.Is(err, failure) {
			t.Fatalf("publication race cache failure=%v", err)
		}
	})

	t.Run("repeated manifest races stop after bounded retries", func(t *testing.T) {
		var client *Client
		var calls atomic.Int32
		source := sourceBehavior{
			info: func(context.Context, string) (CatalogInfo, error) {
				calls.Add(1)
				client.epoch.Add(1)
				return info, nil
			},
			locale: func(context.Context, string, string, string) (Catalog, error) { return catalog, nil },
		}
		client = behaviorClient(t, source, newCoverageCache(t))
		if _, err := client.Translate(ctx, "welcome", Options{}); !errors.Is(err, ErrStaleRevision) || calls.Load() != maxLoadAttempts {
			t.Fatalf("manifest race calls=%d err=%v", calls.Load(), err)
		}
	})
}

var _ Source = sourceBehavior{}
