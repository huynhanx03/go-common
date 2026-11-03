package i18n

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/huynhanx03/go-common/pkg/common/cache"
)

// Manager owns catalog rules while Store owns persistence and migrations.
type Manager struct {
	store Store
	cache *cache.Cache
}

func NewManager(store Store, sharedCache *cache.Cache) (*Manager, error) {
	if isNil(store) || sharedCache == nil {
		return nil, ErrInvalidConfig
	}
	return &Manager{store: store, cache: sharedCache}, nil
}

func (m *Manager) valid() bool { return m != nil && !isNil(m.store) }

func (m *Manager) check(ctx context.Context, domain string, revision int64) error {
	if !m.valid() {
		return ErrInvalidConfig
	}
	if err := contextOK(ctx); err != nil {
		return err
	}
	if ValidateDomain(domain) != nil {
		return ErrInvalidDefinition
	}
	return revisionOK(revision)
}

func backendError(err error) error {
	for _, sentinel := range []error{ErrNotFound, ErrConflict, ErrInvalidConfig, ErrInvalidDefinition, ErrInvalidParams, ErrIncomplete, ErrLimit, ErrClosed, ErrStaleRevision} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return sourceError(err)
}

func (m *Manager) read(ctx context.Context, domain string) (State, error) {
	state, err := m.store.Read(ctx, domain)
	if err != nil {
		return State{}, backendError(err)
	}
	state, err = ValidateState(domain, state)
	if err != nil {
		return State{}, sourceError(err)
	}
	return state, nil
}

func (m *Manager) Get(ctx context.Context, domain string) (State, error) {
	if err := m.check(ctx, domain, 0); err != nil {
		return State{}, err
	}
	return m.read(ctx, domain)
}

// Configure creates or updates the required locale set. A populated domain's
// source locale is immutable so changing it cannot silently reinterpret source text.
func (m *Manager) Configure(ctx context.Context, domain string, config DomainConfig, expected int64) (int64, error) {
	if err := m.check(ctx, domain, expected); err != nil {
		return 0, err
	}
	if err := configOK(config); err != nil {
		return 0, err
	}
	state, err := m.store.Read(ctx, domain)
	if errors.Is(err, ErrNotFound) && expected == 0 {
		locales := slices.Clone(config.Locales)
		slices.Sort(locales)
		draft := Catalog{Domain: domain, Revision: 1, DomainConfig: DomainConfig{DefaultLocale: config.DefaultLocale, Locales: locales}, Keys: []Key{}}
		state = State{Draft: draft}
		if err = m.store.CompareAndSwap(ctx, domain, 0, state); err != nil {
			return 0, backendError(err)
		}
		return 1, nil
	}
	if err != nil {
		return 0, backendError(err)
	}
	state, err = ValidateState(domain, state)
	if err != nil {
		return 0, sourceError(err)
	}
	if state.Draft.Revision != expected {
		return 0, ErrConflict
	}
	if len(state.Draft.Keys) != 0 && state.Draft.DefaultLocale != config.DefaultLocale {
		return 0, ErrInvalidConfig
	}
	state.Draft.DomainConfig = config
	allowed := make(map[string]bool, len(config.Locales))
	for _, locale := range config.Locales {
		allowed[locale] = true
	}
	for i := range state.Draft.Keys {
		for locale := range state.Draft.Keys[i].Translations {
			if !allowed[locale] {
				delete(state.Draft.Keys[i].Translations, locale)
			}
		}
	}
	return m.commit(ctx, domain, expected, state)
}

func (m *Manager) commit(ctx context.Context, domain string, expected int64, state State) (int64, error) {
	if expected >= int64(^uint64(0)>>1)-1 {
		return 0, ErrConflict
	}
	state.Draft.Revision = expected + 1
	state, err := ValidateState(domain, state)
	if err != nil {
		return 0, err
	}
	if err = m.store.CompareAndSwap(ctx, domain, expected, state); err != nil {
		return 0, backendError(err)
	}
	return state.Draft.Revision, nil
}

func (m *Manager) mutate(ctx context.Context, domain string, expected int64, apply func(*State) error) (int64, error) {
	if err := m.check(ctx, domain, expected); err != nil {
		return 0, err
	}
	state, err := m.read(ctx, domain)
	if err != nil {
		return 0, err
	}
	if state.Draft.Revision != expected {
		return 0, ErrConflict
	}
	if err := apply(&state); err != nil {
		return 0, err
	}
	return m.commit(ctx, domain, expected, state)
}

func keyIndex(keys []Key, key string) int {
	return slices.IndexFunc(keys, func(candidate Key) bool { return candidate.Key == key })
}

func upsertDefinition(keys *[]Key, index map[string]int, definition Definition) error {
	i, exists := index[definition.Key]
	if !exists {
		if len(*keys) >= MaxKeys {
			return ErrLimit
		}
		*keys = append(*keys, Key{Definition: definition, Translations: map[string]Translation{}})
		index[definition.Key] = len(*keys) - 1
		return nil
	}
	old := &(*keys)[i]
	if old.Source != definition.Source || old.Plural != definition.Plural {
		for locale, translation := range old.Translations {
			translation.Reviewed = false
			old.Translations[locale] = translation
		}
	}
	old.Definition = definition
	return nil
}

func (m *Manager) PutKey(ctx context.Context, domain string, definition Definition, expected int64) (int64, error) {
	if err := definitionOK(definition); err != nil {
		return 0, err
	}
	return m.mutate(ctx, domain, expected, func(state *State) error {
		index := make(map[string]int, len(state.Draft.Keys))
		for i, key := range state.Draft.Keys {
			index[key.Key] = i
		}
		return upsertDefinition(&state.Draft.Keys, index, definition)
	})
}

// Sync upserts a source manifest and deliberately leaves unmentioned keys alone.
func (m *Manager) Sync(ctx context.Context, domain string, definitions []Definition, expected int64) (int64, error) {
	if len(definitions) > MaxKeys {
		return 0, ErrLimit
	}
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if err := definitionOK(definition); err != nil {
			return 0, err
		}
		if _, ok := seen[definition.Key]; ok {
			return 0, ErrInvalidDefinition
		}
		seen[definition.Key] = struct{}{}
	}
	return m.mutate(ctx, domain, expected, func(state *State) error {
		index := make(map[string]int, len(state.Draft.Keys)+len(definitions))
		for i, key := range state.Draft.Keys {
			index[key.Key] = i
		}
		for _, definition := range definitions {
			if err := upsertDefinition(&state.Draft.Keys, index, definition); err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *Manager) DeleteKey(ctx context.Context, domain, key string, expected int64) (int64, error) {
	if identifier(key) != nil {
		return 0, ErrInvalidDefinition
	}
	return m.mutate(ctx, domain, expected, func(state *State) error {
		i := keyIndex(state.Draft.Keys, key)
		if i < 0 {
			return ErrNotFound
		}
		state.Draft.Keys = append(state.Draft.Keys[:i], state.Draft.Keys[i+1:]...)
		return nil
	})
}

func (m *Manager) PutTranslation(ctx context.Context, domain, key, localeName string, forms Forms, expected int64) (int64, error) {
	if identifier(key) != nil {
		return 0, ErrInvalidDefinition
	}
	if _, err := parseLocale(localeName); err != nil {
		return 0, err
	}
	actualFields, err := formsOK(forms)
	if err != nil {
		return 0, err
	}
	return m.mutate(ctx, domain, expected, func(state *State) error {
		if !slices.Contains(state.Draft.Locales, localeName) || localeName == state.Draft.DefaultLocale {
			return ErrInvalidDefinition
		}
		i := keyIndex(state.Draft.Keys, key)
		if i < 0 {
			return ErrNotFound
		}
		key := state.Draft.Keys[i]
		expectedFields, _ := formsOK(key.Source) // m.read validates the persisted source form before mutation.
		if !slices.Equal(expectedFields, actualFields) {
			return ErrInvalidDefinition
		}
		if !key.Plural {
			for _, value := range formValues(forms)[:5] {
				if value != "" {
					return ErrInvalidDefinition
				}
			}
		}
		key.Translations[localeName] = Translation{Forms: forms}
		return nil
	})
}

func (m *Manager) Review(ctx context.Context, domain, key, localeName string, expected int64) (int64, error) {
	if identifier(key) != nil {
		return 0, ErrInvalidDefinition
	}
	return m.mutate(ctx, domain, expected, func(state *State) error {
		if !slices.Contains(state.Draft.Locales, localeName) || localeName == state.Draft.DefaultLocale {
			return ErrInvalidDefinition
		}
		i := keyIndex(state.Draft.Keys, key)
		if i < 0 {
			return ErrNotFound
		}
		translation, ok := state.Draft.Keys[i].Translations[localeName]
		if !ok {
			return ErrNotFound
		}
		if err := translationReady(state.Draft.Keys[i], localeName, translation.Forms); err != nil {
			return err
		}
		translation.Reviewed = true
		state.Draft.Keys[i].Translations[localeName] = translation
		return nil
	})
}

func (m *Manager) DeleteTranslation(ctx context.Context, domain, key, localeName string, expected int64) (int64, error) {
	if identifier(key) != nil {
		return 0, ErrInvalidDefinition
	}
	return m.mutate(ctx, domain, expected, func(state *State) error {
		if !slices.Contains(state.Draft.Locales, localeName) || localeName == state.Draft.DefaultLocale {
			return ErrInvalidDefinition
		}
		i := keyIndex(state.Draft.Keys, key)
		if i < 0 {
			return ErrNotFound
		}
		if _, ok := state.Draft.Keys[i].Translations[localeName]; !ok {
			return ErrNotFound
		}
		delete(state.Draft.Keys[i].Translations, localeName)
		return nil
	})
}

func (m *Manager) Publish(ctx context.Context, domain string, expected int64) (int64, error) {
	revision, err := m.mutate(ctx, domain, expected, func(state *State) error {
		candidate := state.Draft
		candidate.Revision++
		published, err := NormalizeCatalog(candidate, true)
		if err != nil {
			return err
		}
		state.Published = &published
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revision, m.invalidate(ctx, domain, revision)
}

func (m *Manager) Info(ctx context.Context, domain string) (CatalogInfo, error) {
	return SourceFunc(m.Load).Info(ctx, domain)
}

func (m *Manager) LoadLocale(ctx context.Context, domain, locale, version string) (Catalog, error) {
	return SourceFunc(m.Load).LoadLocale(ctx, domain, locale, version)
}

// Load implements Source and exposes only the last complete publication.
func (m *Manager) Load(ctx context.Context, domain string) (Catalog, error) {
	state, err := m.Get(ctx, domain)
	if err != nil {
		return Catalog{}, err
	}
	if state.Published == nil {
		return Catalog{}, ErrNotFound
	}
	return *state.Published, nil
}

func (m *Manager) Delete(ctx context.Context, domain string, expected int64) error {
	if err := m.check(ctx, domain, expected); err != nil {
		return err
	}
	state, err := m.read(ctx, domain)
	if err != nil {
		return err
	}
	if state.Draft.Revision != expected {
		return ErrConflict
	}
	if err := m.store.Delete(ctx, domain, expected); err != nil {
		return backendError(err)
	}
	return m.invalidate(ctx, domain, expected)
}

func (m *Manager) Coverage(ctx context.Context, domain string) ([]Coverage, error) {
	state, err := m.Get(ctx, domain)
	if err != nil {
		return nil, err
	}
	out := make([]Coverage, 0, len(state.Draft.Locales))
	requiredFormsByLocale := make(map[string][]int, len(state.Draft.Locales))
	for _, locale := range state.Draft.Locales {
		if locale != state.Draft.DefaultLocale {
			tag, _ := parseLocale(locale)
			requiredFormsByLocale[locale] = requiredForms(tag)
		}
		coverage := Coverage{Locale: locale, Total: len(state.Draft.Keys), Missing: []string{}, NeedsReview: []string{}}
		for _, key := range state.Draft.Keys {
			if locale == state.Draft.DefaultLocale {
				coverage.Translated++
				coverage.Reviewed++
				continue
			}
			translation, ok := key.Translations[locale]
			if !ok || translationReadyForForms(key, translation.Forms, requiredFormsByLocale[locale]) != nil {
				coverage.Missing = append(coverage.Missing, key.Key)
				continue
			}
			coverage.Translated++
			if translation.Reviewed {
				coverage.Reviewed++
			} else {
				coverage.NeedsReview = append(coverage.NeedsReview, key.Key)
			}
		}
		out = append(out, coverage)
	}
	return out, nil
}

func (m *Manager) ListKeys(ctx context.Context, domain string, query Query) (Page, error) {
	state, err := m.Get(ctx, domain)
	if err != nil {
		return Page{}, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > MaxPageSize || len(query.Search) > MaxParamBytes || !utf8.ValidString(query.Search) || (query.MissingLocale != "" && !slices.Contains(state.Draft.Locales, query.MissingLocale)) || (query.After != "" && identifier(query.After) != nil) {
		return Page{}, ErrInvalidDefinition
	}
	search := strings.ToLower(query.Search)
	var requiredLocaleForms []int
	if query.MissingLocale != "" && query.MissingLocale != state.Draft.DefaultLocale {
		tag, _ := parseLocale(query.MissingLocale)
		requiredLocaleForms = requiredForms(tag)
	}
	matching := make([]Key, 0, query.Limit+1)
	for _, key := range state.Draft.Keys {
		if query.After != "" && key.Key <= query.After {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(key.Key+" "+key.Description+" "+strings.Join(formValues(key.Source), " ")), search) {
			continue
		}
		if query.MissingLocale != "" {
			if query.MissingLocale == state.Draft.DefaultLocale {
				continue
			}
			translation, exists := key.Translations[query.MissingLocale]
			if exists && translationReadyForForms(key, translation.Forms, requiredLocaleForms) == nil {
				continue
			}
		}
		matching = append(matching, key)
		if len(matching) > query.Limit {
			break
		}
	}
	page := Page{Revision: state.Draft.Revision, Keys: matching}
	if len(matching) > query.Limit {
		page.Next = matching[query.Limit-1].Key
		page.Keys = matching[:query.Limit]
	}
	return page, nil
}

func (m *Manager) Domains(ctx context.Context) ([]string, error) {
	if !m.valid() {
		return nil, ErrInvalidConfig
	}
	if err := contextOK(ctx); err != nil {
		return nil, err
	}
	domains, err := m.store.Domains(ctx)
	if err != nil {
		return nil, backendError(err)
	}
	if len(domains) > MaxDomains {
		return nil, sourceError(ErrLimit)
	}
	seen := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		if ValidateDomain(domain) != nil {
			return nil, sourceError(ErrInvalidDefinition)
		}
		if _, exists := seen[domain]; exists {
			return nil, sourceError(ErrInvalidDefinition)
		}
		seen[domain] = struct{}{}
	}
	sort.Strings(domains)
	return domains, nil
}
