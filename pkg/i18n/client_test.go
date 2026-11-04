package i18n_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
	"github.com/huynhanx03/go-common/pkg/i18n"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func sharedCache(t testing.TB) *cache.Cache {
	t.Helper()
	l1, err := ristretto.New[string, any](ristretto.WithNumCounters(10000), ristretto.WithMaxCost(8<<20))
	if err != nil {
		t.Fatal(err)
	}
	return cacheWithL1(t, l1)
}

func cacheWithL1(t testing.TB, l1 cache.L1Engine) *cache.Cache {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggerConfig{Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := cache.New(cache.Config{L1: l1, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

type failingRevokeL1 struct {
	cache.L1Engine
	fail atomic.Bool
}

func (l1 *failingRevokeL1) Revoke(ctx context.Context, key string) error {
	if l1.fail.Load() {
		return errors.New("cache unavailable")
	}
	return l1.L1Engine.Revoke(ctx, key)
}

type observedSource struct {
	i18n.Source
	infos   atomic.Int32
	locales sync.Map
}

func (s *observedSource) Info(ctx context.Context, domain string) (i18n.CatalogInfo, error) {
	s.infos.Add(1)
	return s.Source.Info(ctx, domain)
}
func (s *observedSource) LoadLocale(ctx context.Context, domain, locale, version string) (i18n.Catalog, error) {
	count, _ := s.locales.LoadOrStore(locale, new(atomic.Int32))
	count.(*atomic.Int32).Add(1)
	return s.Source.LoadLocale(ctx, domain, locale, version)
}

type delayedInfoSource struct {
	i18n.Source
	started chan struct{}
	release chan struct{}
	pause   atomic.Bool
}

func (s *delayedInfoSource) Info(ctx context.Context, domain string) (i18n.CatalogInfo, error) {
	info, err := s.Source.Info(ctx, domain)
	if err != nil || !s.pause.CompareAndSwap(true, false) {
		return info, err
	}
	close(s.started)
	select {
	case <-s.release:
		return info, nil
	case <-ctx.Done():
		return i18n.CatalogInfo{}, ctx.Err()
	}
}

func TestClientLoadsOnlyRequestedLocalesAndReusesCompiledBundles(t *testing.T) {
	m, _ := manager(t)
	published(t, m, "billing")
	source := &observedSource{Source: m}
	store := sharedCache(t)
	client, err := i18n.New(ctx, settings.I18n{Domain: "billing"}, i18n.ClientConfig{Source: source, Cache: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if source.infos.Load() != 0 {
		t.Fatal("construction eagerly loaded the catalog")
	}
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			got, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "en-US,en;q=0.9"})
			if err != nil || got.Text != "Hello" || got.Locale != "en" {
				t.Errorf("translation=%+v err=%v", got, err)
			}
		})
	}
	group.Wait()
	if source.infos.Load() != 1 {
		t.Fatalf("manifest loads=%d", source.infos.Load())
	}
	count, ok := source.locales.Load("en")
	if !ok || count.(*atomic.Int32).Load() != 1 {
		t.Fatal("concurrent misses did not share a locale load")
	}
	if _, ok := source.locales.Load("vi"); ok {
		t.Fatal("unused locale was fetched")
	}
	got, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "fr"})
	if err != nil || got.Text != "Xin chào" || got.Locale != "vi" {
		t.Fatalf("fallback=%+v err=%v", got, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Translate(ctx, "welcome", i18n.Options{}); !errors.Is(err, i18n.ErrClosed) {
		t.Fatalf("closed client: %v", err)
	}
	// Closing a borrower must leave the DI cache usable by another client.
	other, err := i18n.New(ctx, settings.I18n{Domain: "billing"}, i18n.ClientConfig{Source: source, Cache: store})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if got, err := other.Translate(ctx, "welcome", i18n.Options{Locale: "en"}); err != nil || got.Text != "Hello" {
		t.Fatalf("borrowed cache closed: %+v %v", got, err)
	}
}

func TestManifestUsesEachClientsConfiguredCacheLifetime(t *testing.T) {
	m, _ := manager(t)
	published(t, m, "billing")
	store := sharedCache(t)
	source := &observedSource{Source: m}
	longLived, err := i18n.New(ctx, settings.I18n{Domain: "billing", CacheTTLSeconds: 3600}, i18n.ClientConfig{Source: source, Cache: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = longLived.Close() })
	shortLived, err := i18n.New(ctx, settings.I18n{Domain: "billing", CacheTTLSeconds: 1}, i18n.ClientConfig{Source: source, Cache: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shortLived.Close() })

	if _, err := longLived.Translate(ctx, "welcome", i18n.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := shortLived.Translate(ctx, "welcome", i18n.Options{}); err != nil {
		t.Fatal(err)
	}
	if got := source.infos.Load(); got != 1 {
		t.Fatalf("short-lived client did not reuse the shared fresh manifest: Info calls=%d", got)
	}

	time.Sleep(1100 * time.Millisecond)
	if _, err := shortLived.Translate(ctx, "welcome", i18n.Options{}); err != nil {
		t.Fatal(err)
	}
	if got := source.infos.Load(); got != 2 {
		t.Fatalf("long-lived writer extended short-lived manifest: Info calls=%d, want 2", got)
	}
}

func TestCatalogVersionDoesNotDependOnTranslationMapIterationOrder(t *testing.T) {
	catalog := i18n.Catalog{
		Domain: "billing", Revision: 1,
		DomainConfig: i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en", "fr"}},
		Keys: []i18n.Key{{
			Definition:   i18n.Definition{Key: "welcome", Source: i18n.Forms{Other: "Xin chào"}},
			Translations: map[string]i18n.Translation{},
		}},
	}
	catalog.Keys[0].Translations["en"] = i18n.Translation{Forms: i18n.Forms{Other: "Hello"}, Reviewed: true}
	catalog.Keys[0].Translations["fr"] = i18n.Translation{Forms: i18n.Forms{Other: "Bonjour"}, Reviewed: true}
	first, err := i18n.SourceFunc(func(context.Context, string) (i18n.Catalog, error) { return catalog, nil }).Info(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}

	reordered := catalog
	reordered.Keys = append([]i18n.Key(nil), catalog.Keys...)
	reordered.Keys[0].Translations = map[string]i18n.Translation{}
	reordered.Keys[0].Translations["fr"] = catalog.Keys[0].Translations["fr"]
	reordered.Keys[0].Translations["en"] = catalog.Keys[0].Translations["en"]
	second, err := i18n.SourceFunc(func(context.Context, string) (i18n.Catalog, error) { return reordered, nil }).Info(ctx, "billing")
	if err != nil || first.Version != second.Version {
		t.Fatalf("equivalent catalog versions differ: first=%q second=%q err=%v", first.Version, second.Version, err)
	}
}

func TestPublishedChangesReachOtherClientsAfterTheirCacheTTL(t *testing.T) {
	m, err := i18n.NewManager(newMemoryStore(), sharedCache(t))
	if err != nil {
		t.Fatal(err)
	}
	revision := published(t, m, "billing")
	clients := make([]*i18n.Client, 2)
	for i := range clients {
		clients[i], err = i18n.New(ctx, settings.I18n{Domain: "billing", CacheTTLSeconds: 1}, i18n.ClientConfig{Source: m, Cache: sharedCache(t)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clients[i].Close() })
		if got, err := clients[i].Translate(ctx, "welcome", i18n.Options{Locale: "en"}); err != nil || got.Text != "Hello" {
			t.Fatalf("initial=%+v %v", got, err)
		}
	}
	revision, err = m.PutTranslation(ctx, "billing", "welcome", "en", i18n.Forms{Other: "Hello again"}, revision)
	revision = mustRev(t, revision, err)
	for _, client := range clients {
		if got, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "en"}); err != nil || got.Text != "Hello" {
			t.Fatalf("draft leaked=%+v %v", got, err)
		}
	}
	revision, err = m.Review(ctx, "billing", "welcome", "en", revision)
	revision = mustRev(t, revision, err)
	revision, err = m.Publish(ctx, "billing", revision)
	revision = mustRev(t, revision, err)
	time.Sleep(1100 * time.Millisecond)
	for _, client := range clients {
		if got, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "en"}); err != nil || got.Text != "Hello again" || got.Revision != revision {
			t.Fatalf("published catalog did not refresh after cache TTL=%+v %v", got, err)
		}
	}
	if err := m.Delete(ctx, "billing", revision); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	for _, client := range clients {
		if _, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "en"}); !errors.Is(err, i18n.ErrNotFound) {
			t.Fatalf("deleted catalog served: %v", err)
		}
	}
	// Recreated catalogs may reuse a revision number; their content must not reuse an old bundle.
	published(t, m, "billing")
	for range clients {
		client, err := i18n.New(ctx, settings.I18n{Domain: "billing"}, i18n.ClientConfig{Source: m, Cache: sharedCache(t)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		if got, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "en"}); err != nil || got.Text != "Hello" {
			t.Fatalf("recreated=%+v %v", got, err)
		}
	}
}

func TestPublishDuringManifestLoadCannotRestoreOrReturnOldCatalog(t *testing.T) {
	manager, err := i18n.NewManager(newMemoryStore(), sharedCache(t))
	if err != nil {
		t.Fatal(err)
	}
	revision := published(t, manager, "billing")
	source := &delayedInfoSource{Source: manager, started: make(chan struct{}), release: make(chan struct{})}
	source.pause.Store(true)
	client, err := i18n.New(ctx, settings.I18n{Domain: "billing"}, i18n.ClientConfig{Source: source, Cache: sharedCache(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	result := make(chan struct {
		value i18n.Result
		err   error
	}, 1)
	go func() {
		value, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "en"})
		result <- struct {
			value i18n.Result
			err   error
		}{value: value, err: err}
	}()
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("manifest load did not start")
	}

	revision, err = manager.PutTranslation(ctx, "billing", "welcome", "en", i18n.Forms{Other: "Published while loading"}, revision)
	revision = mustRev(t, revision, err)
	revision, err = manager.Review(ctx, "billing", "welcome", "en", revision)
	revision = mustRev(t, revision, err)
	revision, err = manager.Publish(ctx, "billing", revision)
	revision = mustRev(t, revision, err)
	close(source.release)

	select {
	case got := <-result:
		if got.err != nil || got.value.Text != "Published while loading" || got.value.Revision != revision {
			t.Fatalf("request returned stale publication: result=%+v err=%v", got.value, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("translation did not retry after publication")
	}
}

func TestCommittedPublicationCanRetryInvalidationWithoutRepeatingWrite(t *testing.T) {
	inner, err := ristretto.New[string, any](ristretto.WithNumCounters(10000), ristretto.WithMaxCost(8<<20))
	if err != nil {
		t.Fatal(err)
	}
	l1 := &failingRevokeL1{L1Engine: inner}
	manager, err := i18n.NewManager(newMemoryStore(), cacheWithL1(t, l1))
	if err != nil {
		t.Fatal(err)
	}
	revision := published(t, manager, "billing")
	revision, err = manager.PutTranslation(ctx, "billing", "welcome", "en", i18n.Forms{Other: "Hello again"}, revision)
	revision = mustRev(t, revision, err)
	revision, err = manager.Review(ctx, "billing", "welcome", "en", revision)
	revision = mustRev(t, revision, err)

	l1.fail.Store(true)
	committedRevision, err := manager.Publish(ctx, "billing", revision)
	var committed *i18n.CommittedError
	if committedRevision != revision+1 || !errors.Is(err, i18n.ErrInvalidation) || !errors.As(err, &committed) || committed.Revision != committedRevision {
		t.Fatalf("publish revision=%d error=%v", committedRevision, err)
	}
	publishedCatalog, err := manager.Load(ctx, "billing")
	var welcome string
	for _, key := range publishedCatalog.Keys {
		if key.Key == "welcome" {
			welcome = key.Translations["en"].Forms.Other
		}
	}
	if err != nil || publishedCatalog.Revision != committedRevision || welcome != "Hello again" {
		t.Fatalf("write was not committed: catalog=%+v err=%v", publishedCatalog, err)
	}

	l1.fail.Store(false)
	if err := manager.Invalidate(ctx, "billing", committedRevision); err != nil {
		t.Fatalf("retry invalidation: %v", err)
	}
	if state, err := manager.Get(ctx, "billing"); err != nil || state.Draft.Revision != committedRevision {
		t.Fatalf("invalidation retry repeated or changed the write: state=%+v err=%v", state, err)
	}
}
