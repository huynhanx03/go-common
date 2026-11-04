package i18n

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
)

func TestManagerRejectsInvalidDependenciesAndPersistedState(t *testing.T) {
	if _, err := NewManager(nil, newCoverageCache(t)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil Store accepted: %v", err)
	}
	store := newCoverageStore()
	if _, err := NewManager(store, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil shared cache accepted: %v", err)
	}
	var nilStore *coverageStore
	if _, err := NewManager(nilStore, newCoverageCache(t)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("typed nil Store accepted: %v", err)
	}

	manager := newCoverageManager(t, store)
	store.states["billing"] = State{Draft: Catalog{
		Domain: "other", Revision: 1,
		DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}},
	}}
	if _, err := manager.Configure(context.Background(), "billing", DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}, 1); !errors.Is(err, ErrSource) {
		t.Fatalf("corrupt persisted catalog was accepted for reconfiguration: %v", err)
	}
}

func TestManagerCommitAndMutatePreserveRevisionAndValidationBoundaries(t *testing.T) {
	store := newCoverageStore()
	manager := newCoverageManager(t, store)
	invalid := State{Draft: Catalog{Domain: "other", Revision: 1, DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}}}
	if _, err := manager.commit(context.Background(), "billing", 0, invalid); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid state committed: %v", err)
	}
	if _, err := manager.mutate(nil, "billing", 0, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("mutation without request context: %v", err)
	}
	if _, err := manager.mutate(context.Background(), "billing", 0, func(*State) error { return ErrIncomplete }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing catalog mutation error=%v", err)
	}

	revision := configureForTest(t, manager, "billing", "en")
	if _, err := manager.mutate(context.Background(), "billing", revision, func(*State) error { return ErrIncomplete }); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("domain rule failure was swallowed: %v", err)
	}
	if _, err := manager.commit(context.Background(), "billing", math.MaxInt64-1, State{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("revision overflow was accepted: %v", err)
	}
}

func TestCatalogKeyAndManifestLimitsAreEnforced(t *testing.T) {
	keys := make([]Key, MaxKeys)
	index := make(map[string]int)
	if err := upsertDefinition(&keys, index, Definition{Key: "last", Source: Forms{Other: "last"}}); !errors.Is(err, ErrLimit) {
		t.Fatalf("key limit error=%v", err)
	}
	manager := newCoverageManager(t, newCoverageStore())
	if _, err := manager.Sync(context.Background(), "billing", make([]Definition, MaxKeys+1), 0); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized source manifest error=%v", err)
	}
	if _, err := manager.Sync(context.Background(), "billing", []Definition{{Key: "bad key", Source: Forms{Other: "x"}}}, 0); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid source manifest entry error=%v", err)
	}

	store := newCoverageStore()
	manager = newCoverageManager(t, store)
	keys = make([]Key, MaxKeys)
	for i := range keys {
		keys[i] = Key{Definition: Definition{Key: fmt.Sprintf("key-%04d", i), Source: Forms{Other: "x"}}, Translations: map[string]Translation{}}
	}
	store.states["full"] = State{Draft: Catalog{
		Domain: "full", Revision: 1,
		DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}},
		Keys:         keys,
	}}
	if _, err := manager.Sync(context.Background(), "full", []Definition{{Key: "overflow", Source: Forms{Other: "x"}}}, 1); !errors.Is(err, ErrLimit) {
		t.Fatalf("adding beyond persisted key limit error=%v", err)
	}
}

func TestManagerTranslationReviewDeletionAndCoverageLifecycle(t *testing.T) {
	manager := newCoverageManager(t, newCoverageStore())
	ctx := context.Background()
	revision := configureForTest(t, manager, "billing", "en", "fr")
	revision, err := manager.PutKey(ctx, "billing", Definition{Key: "invoice", Source: Forms{Other: "Invoice"}}, revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "invoice", "fr_CA", Forms{Other: "Facture"}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("non-canonical locale accepted: %v", err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "invoice", "fr", Forms{Other: `{{.Name`}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("malformed translation accepted: %v", err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "invoice", "fr", Forms{One: "facture", Other: "factures"}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("non-plural translation accepted plural forms: %v", err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "bad key", "fr", Forms{Other: "Facture"}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid key accepted for translation: %v", err)
	}
	if _, err := manager.Review(ctx, "billing", "bad key", "fr", revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid key accepted for review: %v", err)
	}
	if _, err := manager.Review(ctx, "billing", "invoice", "en", revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("source locale review accepted: %v", err)
	}
	if _, err := manager.DeleteKey(ctx, "billing", "bad key", revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid key deletion error=%v", err)
	}
	if _, err := manager.Review(ctx, "billing", "missing", "fr", revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("review for missing key error=%v", err)
	}

	// Empty drafts are valid to save, but cannot be marked reviewed or published.
	revision, err = manager.PutTranslation(ctx, "billing", "invoice", "fr", Forms{}, revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Review(ctx, "billing", "invoice", "fr", revision); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("incomplete translation was reviewed: %v", err)
	}
	revision, err = manager.PutTranslation(ctx, "billing", "invoice", "fr", Forms{Other: "Facture"}, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.Review(ctx, "billing", "invoice", "fr", revision)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := manager.Coverage(ctx, "billing")
	if err != nil || coverage[0].Reviewed != 1 {
		t.Fatalf("reviewed translation coverage=%+v err=%v", coverage, err)
	}
	if _, err := manager.DeleteTranslation(ctx, "billing", "invoice", "en", revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("source locale deletion accepted: %v", err)
	}
	if _, err := manager.DeleteTranslation(ctx, "billing", "bad key", "fr", revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid key translation deletion error=%v", err)
	}
	if _, err := manager.DeleteTranslation(ctx, "billing", "missing", "fr", revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("translation deletion for missing key error=%v", err)
	}
	revision, err = manager.DeleteTranslation(ctx, "billing", "invoice", "fr", revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DeleteTranslation(ctx, "billing", "invoice", "fr", revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing translation deletion error=%v", err)
	}
}

func TestManagerReadAndInventoryErrorsRespectRequestContext(t *testing.T) {
	store := newCoverageStore()
	manager := newCoverageManager(t, store)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Coverage(canceled, "billing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("coverage ignored cancellation: %v", err)
	}
	if _, err := manager.ListKeys(canceled, "billing", Query{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("key search ignored cancellation: %v", err)
	}
	if _, err := manager.Domains(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("domain inventory ignored cancellation: %v", err)
	}
	if err := manager.Delete(canceled, "billing", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete ignored cancellation: %v", err)
	}
	store.readErr = errors.New("database unavailable")
	if _, err := manager.ListKeys(context.Background(), "billing", Query{}); !errors.Is(err, ErrSource) {
		t.Fatalf("key search storage error=%v", err)
	}
	store.readErr = nil
	if _, err := manager.Load(context.Background(), "billing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unconfigured catalog load error=%v", err)
	}
	configureForTest(t, manager, "draft-only", "en")
	if _, err := manager.Load(context.Background(), "draft-only"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unpublished catalog load error=%v", err)
	}
	store.readErr = errors.New("database unavailable")
	if err := manager.Delete(context.Background(), "draft-only", 1); !errors.Is(err, ErrSource) {
		t.Fatalf("delete read failure error=%v", err)
	}
	store.readErr = nil
	if _, err := manager.PutKey(context.Background(), "bad domain", Definition{Key: "valid", Source: Forms{Other: "valid"}}, 0); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid domain mutation error=%v", err)
	}
}
