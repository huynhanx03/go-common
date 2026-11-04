package i18n

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func configureForTest(t *testing.T, manager *Manager, domain string, locales ...string) int64 {
	t.Helper()
	revision, err := manager.Configure(context.Background(), domain, DomainConfig{DefaultLocale: locales[0], Locales: locales}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestManagerReconfigurationPrunesRemovedLocalesAndFencesSourceLocale(t *testing.T) {
	store := newCoverageStore()
	manager := newCoverageManager(t, store)
	revision := configureForTest(t, manager, "billing", "vi", "en", "fr")
	revision, err := manager.PutKey(context.Background(), "billing", Definition{
		Key: "invoice.ready", Source: Forms{Other: "Hóa đơn {{.Number}}"},
	}, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.PutTranslation(context.Background(), "billing", "invoice.ready", "en", Forms{Other: "Invoice {{.Number}}"}, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.PutTranslation(context.Background(), "billing", "invoice.ready", "fr", Forms{Other: "Facture {{.Number}}"}, revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Configure(context.Background(), "billing", DomainConfig{DefaultLocale: "en", Locales: []string{"en", "vi"}}, revision); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("changing source locale on a populated domain: %v", err)
	}
	revision, err = manager.Configure(context.Background(), "billing", DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, revision)
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Get(context.Background(), "billing")
	if err != nil {
		t.Fatal(err)
	}
	translation := state.Draft.Keys[0].Translations
	if revision != 5 || len(state.Draft.Locales) != 2 || translation["fr"].Forms.Other != "" {
		t.Fatalf("reconfiguration revision=%d locales=%v translations=%v", revision, state.Draft.Locales, translation)
	}
	if _, err := manager.Configure(context.Background(), "billing", DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, revision-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reconfiguration was accepted: %v", err)
	}
}

func TestManagerCoverageAndFindKeysReflectTranslationState(t *testing.T) {
	manager := newCoverageManager(t, newCoverageStore())
	revision := configureForTest(t, manager, "billing", "vi", "en")
	definitions := []Definition{
		{Key: "invoice.created", Description: "Invoice created", Source: Forms{Other: "Hóa đơn đã tạo"}},
		{Key: "invoice.failed", Description: "Invoice failed", Source: Forms{Other: "Hóa đơn thất bại"}},
		{Key: "payment.received", Description: "Payment notice", Source: Forms{Other: "Đã nhận thanh toán"}},
	}
	revision, err := manager.Sync(context.Background(), "billing", definitions, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.PutTranslation(context.Background(), "billing", "invoice.created", "en", Forms{Other: "Invoice created"}, revision)
	if err != nil {
		t.Fatal(err)
	}

	coverage, err := manager.Coverage(context.Background(), "billing")
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != 2 || coverage[0].Locale != "en" || coverage[1].Locale != "vi" || coverage[1].Translated != 3 || coverage[1].Reviewed != 3 {
		t.Fatalf("default locale coverage=%+v", coverage)
	}
	english := coverage[0]
	if english.Locale != "en" || english.Translated != 1 || english.Reviewed != 0 || len(english.Missing) != 2 || len(english.NeedsReview) != 1 || english.NeedsReview[0] != "invoice.created" {
		t.Fatalf("translation coverage=%+v", english)
	}

	page, err := manager.ListKeys(context.Background(), "billing", Query{Search: "PAYMENT NOTICE", Limit: 10})
	if err != nil || len(page.Keys) != 1 || page.Keys[0].Key != "payment.received" {
		t.Fatalf("case-insensitive description search page=%+v err=%v", page, err)
	}
	page, err = manager.ListKeys(context.Background(), "billing", Query{Limit: 1})
	if err != nil || len(page.Keys) != 1 || page.Next != "invoice.created" {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	page, err = manager.ListKeys(context.Background(), "billing", Query{After: page.Next, Limit: 1})
	if err != nil || len(page.Keys) != 1 || page.Keys[0].Key != "invoice.failed" || page.Next != "invoice.failed" {
		t.Fatalf("second page=%+v err=%v", page, err)
	}
	page, err = manager.ListKeys(context.Background(), "billing", Query{MissingLocale: "en", Limit: 10})
	if err != nil || len(page.Keys) != 2 {
		t.Fatalf("keys missing English translation=%+v err=%v", page, err)
	}
	page, err = manager.ListKeys(context.Background(), "billing", Query{MissingLocale: "vi", Limit: 10})
	if err != nil || len(page.Keys) != 0 {
		t.Fatalf("default locale unexpectedly had missing translations=%+v err=%v", page, err)
	}
	if _, err := manager.ListKeys(context.Background(), "billing", Query{Limit: MaxPageSize + 1}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("oversized page limit accepted: %v", err)
	}
	if _, err := manager.ListKeys(context.Background(), "billing", Query{MissingLocale: "fr"}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("unsupported missing locale accepted: %v", err)
	}
	if _, err := manager.ListKeys(context.Background(), "billing", Query{After: "bad key"}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid cursor accepted: %v", err)
	}
	if _, err := manager.ListKeys(context.Background(), "billing", Query{Search: strings.Repeat("x", MaxParamBytes+1)}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("oversized search accepted: %v", err)
	}
}

func TestManagerWriteFailuresAndPreconditionsStayDomainScoped(t *testing.T) {
	store := newCoverageStore()
	manager := newCoverageManager(t, store)
	ctx := context.Background()
	revision := configureForTest(t, manager, "billing", "vi", "en")
	definition := Definition{Key: "welcome", Source: Forms{Other: "Xin chào"}}

	if _, err := manager.PutTranslation(ctx, "billing", "welcome", "vi", Forms{Other: "Hello"}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("source locale accepted as translation: %v", err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "welcome", "fr", Forms{Other: "Hello"}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("unconfigured locale accepted: %v", err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "missing", "en", Forms{Other: "Hello"}, revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("translation for missing key: %v", err)
	}
	revision, err := manager.PutKey(ctx, "billing", definition, revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "welcome", "en", Forms{Other: "Hello {{.Name}}"}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("placeholder mismatch accepted: %v", err)
	}
	if _, err := manager.Review(ctx, "billing", "welcome", "en", revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("review of missing translation: %v", err)
	}
	if _, err := manager.DeleteTranslation(ctx, "billing", "welcome", "en", revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete of missing translation: %v", err)
	}
	if _, err := manager.DeleteKey(ctx, "billing", "missing", revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing key: %v", err)
	}
	if _, err := manager.DeleteKey(ctx, "billing", "welcome", revision-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale key deletion: %v", err)
	}
	if _, err := manager.Publish(ctx, "billing", revision); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("published incomplete translation: %v", err)
	}
	if err := manager.Delete(ctx, "billing", revision-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale domain deletion: %v", err)
	}
	if _, err := manager.PutTranslation(ctx, "billing", "welcome", "en", Forms{Other: "bad {{if .Name}}"}, revision); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("malformed translation accepted: %v", err)
	}
}

func TestManagerMapsStorageFailuresAndRejectsCorruptRows(t *testing.T) {
	store := newCoverageStore()
	manager := newCoverageManager(t, store)
	ctx := context.Background()
	store.readErr = errors.New("database unavailable")
	if _, err := manager.Get(ctx, "billing"); !errors.Is(err, ErrSource) {
		t.Fatalf("storage read failure was not redacted: %v", err)
	}
	if _, err := manager.Configure(ctx, "billing", DomainConfig{DefaultLocale: "vi", Locales: []string{"vi"}}, 0); !errors.Is(err, ErrSource) {
		t.Fatalf("configure read failure was not mapped: %v", err)
	}
	store.readErr = ErrNotFound
	store.writeErr = errors.New("database unavailable")
	if _, err := manager.Configure(ctx, "billing", DomainConfig{DefaultLocale: "vi", Locales: []string{"vi"}}, 0); !errors.Is(err, ErrSource) {
		t.Fatalf("configure write failure was not mapped: %v", err)
	}
	store.writeErr = nil
	revision := configureForTest(t, manager, "billing", "vi", "en")
	store.readErr = nil
	store.writeErr = errors.New("database unavailable")
	if _, err := manager.PutKey(ctx, "billing", Definition{Key: "welcome", Source: Forms{Other: "Xin chào"}}, revision); !errors.Is(err, ErrSource) {
		t.Fatalf("mutation write failure was not mapped: %v", err)
	}
	store.writeErr = nil
	store.deleteErr = errors.New("database unavailable")
	if err := manager.Delete(ctx, "billing", revision); !errors.Is(err, ErrSource) {
		t.Fatalf("delete failure was not mapped: %v", err)
	}
	store.deleteErr = nil
	store.states["billing"] = State{Draft: Catalog{Domain: "different", Revision: 1, DomainConfig: DomainConfig{DefaultLocale: "vi", Locales: []string{"vi"}}}}
	if _, err := manager.Get(ctx, "billing"); !errors.Is(err, ErrSource) {
		t.Fatalf("corrupt row was not hidden as source failure: %v", err)
	}
}

func TestManagerChecksBoundariesBeforeCallingStore(t *testing.T) {
	store := newCoverageStore()
	manager := newCoverageManager(t, store)
	if _, err := manager.Get(nil, "billing"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil request context: %v", err)
	}
	if _, err := manager.Get(context.Background(), "bad domain"); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid domain: %v", err)
	}
	if _, err := manager.PutKey(context.Background(), "billing", Definition{Key: "!", Source: Forms{Other: "x"}}, 0); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid key definition: %v", err)
	}
	if _, err := manager.Configure(context.Background(), "billing", DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}, -1); !errors.Is(err, ErrConflict) {
		t.Fatalf("negative revision: %v", err)
	}
	if _, err := manager.Configure(context.Background(), "billing", DomainConfig{}, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid locale config: %v", err)
	}
	var missing *Manager
	if _, err := missing.Get(context.Background(), "billing"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil manager call: %v", err)
	}
	if _, err := missing.Domains(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil manager domains: %v", err)
	}
	if _, err := manager.Configure(context.Background(), "billing", DomainConfig{DefaultLocale: "vi", Locales: []string{"vi"}}, math.MaxInt64); !errors.Is(err, ErrConflict) {
		t.Fatalf("maximum revision: %v", err)
	}
}

func TestManagerRejectsRevisionExhaustionAndDomainInventoryCorruption(t *testing.T) {
	store := newCoverageStore()
	manager := newCoverageManager(t, store)
	store.states["billing"] = State{Draft: Catalog{
		Domain: "billing", Revision: math.MaxInt64 - 1,
		DomainConfig: DomainConfig{DefaultLocale: "vi", Locales: []string{"vi"}}, Keys: []Key{},
	}}
	if _, err := manager.Configure(context.Background(), "billing", DomainConfig{DefaultLocale: "vi", Locales: []string{"vi"}}, math.MaxInt64-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("revision exhaustion: %v", err)
	}
	store.domainsErr = errors.New("database unavailable")
	if _, err := manager.Domains(context.Background()); !errors.Is(err, ErrSource) {
		t.Fatalf("domain query failure: %v", err)
	}
	store.domainsErr = nil
	store.domainsSeed = []string{"zeta", "alpha", "alpha"}
	if _, err := manager.Domains(context.Background()); !errors.Is(err, ErrSource) {
		t.Fatalf("duplicate domain rows: %v", err)
	}
	store.domainsSeed = []string{"bad domain"}
	if _, err := manager.Domains(context.Background()); !errors.Is(err, ErrSource) {
		t.Fatalf("invalid persisted domain: %v", err)
	}
	store.domainsSeed = make([]string, MaxDomains+1)
	for i := range store.domainsSeed {
		store.domainsSeed[i] = fmt.Sprintf("domain-%04d", i)
	}
	if _, err := manager.Domains(context.Background()); !errors.Is(err, ErrSource) || !errors.Is(err, ErrLimit) {
		t.Fatalf("domain inventory limit: %v", err)
	}
}
