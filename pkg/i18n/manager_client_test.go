package i18n_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/i18n"
	"github.com/huynhanx03/go-common/pkg/settings"
)

type memoryStore struct {
	mu     sync.Mutex
	states map[string]i18n.State
}

func newMemoryStore() *memoryStore { return &memoryStore{states: make(map[string]i18n.State)} }
func copyState(s i18n.State) i18n.State {
	b, _ := jsoncodec.Marshal(s)
	var out i18n.State
	_ = jsoncodec.Unmarshal(b, &out)
	return out
}
func (s *memoryStore) Read(_ context.Context, d string) (i18n.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.states[d]
	if !ok {
		return i18n.State{}, i18n.ErrNotFound
	}
	return copyState(v), nil
}
func (s *memoryStore) CompareAndSwap(_ context.Context, d string, rev int64, next i18n.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states[d].Draft.Revision != rev {
		return i18n.ErrConflict
	}
	s.states[d] = copyState(next)
	return nil
}
func (s *memoryStore) Delete(_ context.Context, d string, rev int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.states[d]
	if !ok {
		return i18n.ErrNotFound
	}
	if v.Draft.Revision != rev {
		return i18n.ErrConflict
	}
	delete(s.states, d)
	return nil
}
func (s *memoryStore) Domains(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for d := range s.states {
		out = append(out, d)
	}
	return out, nil
}

var ctx = context.Background()

func manager(t testing.TB) (*i18n.Manager, *memoryStore) {
	t.Helper()
	store := newMemoryStore()
	m, err := i18n.NewManager(store, sharedCache(t))
	if err != nil {
		t.Fatal(err)
	}
	return m, store
}
func mustRev(t testing.TB, rev int64, err error) int64 {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return rev
}
func configured(t testing.TB, m *i18n.Manager, domain string) int64 {
	t.Helper()
	rev, err := m.Configure(ctx, domain, i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, 0)
	return mustRev(t, rev, err)
}
func published(t testing.TB, m *i18n.Manager, domain string) int64 {
	t.Helper()
	r := configured(t, m, domain)
	r, e := m.Sync(ctx, domain, []i18n.Definition{
		{Key: "payment.received", Source: i18n.Forms{Other: "Chào {{.Name}}, đã nhận {{.Amount}}."}},
		{Key: "items", Plural: true, Source: i18n.Forms{Other: "{{.Count}} sản phẩm"}},
		{Key: "welcome", Source: i18n.Forms{Other: "Xin chào"}},
	}, r)
	r = mustRev(t, r, e)
	for key, forms := range map[string]i18n.Forms{
		"payment.received": {Other: "Hello {{.Name}}, received {{.Amount}}."},
		"items":            {One: "{{.Count}} item", Other: "{{.Count}} items"},
		"welcome":          {Other: "Hello"},
	} {
		r, e = m.PutTranslation(ctx, domain, key, "en", forms, r)
		r = mustRev(t, r, e)
		r, e = m.Review(ctx, domain, key, "en", r)
		r = mustRev(t, r, e)
	}
	r, e = m.Publish(ctx, domain, r)
	return mustRev(t, r, e)
}

// Breaks if publishing incomplete drafts, accepting stale writes, retaining reviews
// after source edits, or exposing drafts through the published Source.
func TestCatalogLifecycleRequiresCompleteReviewedTranslations(t *testing.T) {
	m, _ := manager(t)
	r := configured(t, m, "billing")
	for _, definition := range []i18n.Definition{
		{Key: "nul.source", Source: i18n.Forms{Other: "bad\x00text"}},
		{Key: "nul.description", Description: "bad\x00text", Source: i18n.Forms{Other: "valid"}},
	} {
		if _, err := m.PutKey(ctx, "billing", definition, r); !errors.Is(err, i18n.ErrInvalidDefinition) {
			t.Fatalf("NUL-containing definition accepted: %+v err=%v", definition, err)
		}
	}
	r, e := m.PutKey(ctx, "billing", i18n.Definition{Key: "payment.failed", Source: i18n.Forms{Other: "Thanh toán {{.Amount}} thất bại"}}, r)
	r = mustRev(t, r, e)
	if _, e = m.Publish(ctx, "billing", r); !errors.Is(e, i18n.ErrIncomplete) {
		t.Fatalf("publish incomplete: %v", e)
	}
	coverage, e := m.Coverage(ctx, "billing")
	if e != nil || len(coverage) != 2 || len(coverage[0].Missing)+len(coverage[1].Missing) != 1 {
		t.Fatalf("coverage=%+v err=%v", coverage, e)
	}
	if _, e = m.PutTranslation(ctx, "billing", "payment.failed", "en", i18n.Forms{Other: "Failed {{.Wrong}}"}, r); !errors.Is(e, i18n.ErrInvalidDefinition) {
		t.Fatalf("placeholder mismatch: %v", e)
	}
	r, e = m.PutTranslation(ctx, "billing", "payment.failed", "en", i18n.Forms{Other: "Payment of {{.Amount}} failed"}, r)
	r = mustRev(t, r, e)
	page, e := m.ListKeys(ctx, "billing", i18n.Query{MissingLocale: "en"})
	if e != nil || len(page.Keys) != 0 {
		t.Fatalf("complete translation incorrectly appeared missing: %+v err=%v", page, e)
	}
	if _, e = m.Publish(ctx, "billing", r); !errors.Is(e, i18n.ErrIncomplete) {
		t.Fatalf("publish unreviewed: %v", e)
	}
	r, e = m.Review(ctx, "billing", "payment.failed", "en", r)
	r = mustRev(t, r, e)
	if _, e = m.DeleteKey(ctx, "billing", "payment.failed", r-1); !errors.Is(e, i18n.ErrConflict) {
		t.Fatalf("stale delete: %v", e)
	}
	r, e = m.Publish(ctx, "billing", r)
	r = mustRev(t, r, e)
	live, e := m.Load(ctx, "billing")
	if e != nil || live.Revision != r {
		t.Fatalf("published=%+v err=%v", live, e)
	}
	r, e = m.PutKey(ctx, "billing", i18n.Definition{Key: "payment.failed", Source: i18n.Forms{Other: "Thanh toán {{.Amount}} bị từ chối"}}, r)
	r = mustRev(t, r, e)
	if _, e = m.Publish(ctx, "billing", r); !errors.Is(e, i18n.ErrIncomplete) {
		t.Fatalf("source edit retained review: %v", e)
	}
	after, e := m.Load(ctx, "billing")
	if e != nil || after.Keys[0].Source.Other != live.Keys[0].Source.Other {
		t.Fatalf("draft leaked: %+v %v", after, e)
	}
}

func TestSyncUpsertsManifestWithoutDeletingUnlistedKeys(t *testing.T) {
	m, _ := manager(t)
	revision := configured(t, m, "catalog")
	revision, err := m.Sync(ctx, "catalog", []i18n.Definition{
		{Key: "invoice.created", Source: i18n.Forms{Other: "Invoice created"}},
		{Key: "invoice.failed", Source: i18n.Forms{Other: "Invoice failed"}},
	}, revision)
	revision = mustRev(t, revision, err)
	revision, err = m.Sync(ctx, "catalog", []i18n.Definition{
		{Key: "invoice.failed", Source: i18n.Forms{Other: "Invoice rejected"}},
	}, revision)
	revision = mustRev(t, revision, err)
	state, err := m.Get(ctx, "catalog")
	if err != nil || revision != 3 || len(state.Draft.Keys) != 2 {
		t.Fatalf("sync unexpectedly removed keys: revision=%d state=%+v err=%v", revision, state, err)
	}
	if _, err := m.Sync(ctx, "catalog", []i18n.Definition{
		{Key: "duplicate", Source: i18n.Forms{Other: "first"}},
		{Key: "duplicate", Source: i18n.Forms{Other: "second"}},
	}, revision); !errors.Is(err, i18n.ErrInvalidDefinition) {
		t.Fatalf("duplicate manifest key error=%v", err)
	}
	state, err = m.Get(ctx, "catalog")
	if err != nil || state.Draft.Revision != revision || len(state.Draft.Keys) != 2 {
		t.Fatalf("rejected manifest changed state: %+v err=%v", state, err)
	}
}

func TestConcurrentCatalogWritersCannotOverwriteTheSameRevision(t *testing.T) {
	m, _ := manager(t)
	revision := configured(t, m, "race")
	results := make(chan error, 2)
	for _, key := range []string{"first", "second"} {
		go func(key string) {
			_, err := m.PutKey(ctx, "race", i18n.Definition{Key: key, Source: i18n.Forms{Other: key}}, revision)
			results <- err
		}(key)
	}
	successes, conflicts := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, i18n.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent write error: %v", err)
		}
	}
	state, err := m.Get(ctx, "race")
	if err != nil || successes != 1 || conflicts != 1 || state.Draft.Revision != revision+1 || len(state.Draft.Keys) != 1 {
		t.Fatalf("writers successes=%d conflicts=%d state=%+v err=%v", successes, conflicts, state, err)
	}
}

// Breaks if the runtime performs source I/O per lookup, mutates caller params,
// chooses the wrong plural, loses minor units or formats fallback in another locale.
func TestRuntimeLocalParametersPluralMoneyAndFallback(t *testing.T) {
	m, _ := manager(t)
	revision := published(t, m, "billing")
	loads := 0
	source := i18n.SourceFunc(func(c context.Context, d string) (i18n.Catalog, error) { loads++; return m.Load(c, d) })
	c, e := i18n.New(ctx, settings.I18n{Domain: "billing", CacheTTLSeconds: 3600}, i18n.ClientConfig{Source: source, Cache: sharedCache(t)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	params := map[string]any{"Name": "An", "Amount": i18n.Money{MinorUnits: 123456, Currency: "USD"}}
	got, e := c.Translate(ctx, "payment.received", i18n.Options{Locale: "en-US,en;q=0.9", Params: params})
	if e != nil || got.Text != "Hello An, received 1,234.56 USD." || got.Locale != "en" || got.Revision != revision {
		t.Fatalf("translation=%+v err=%v", got, e)
	}
	if _, ok := params["Amount"].(i18n.Money); !ok {
		t.Fatal("caller params mutated")
	}
	for _, test := range []struct {
		count int
		want  string
	}{{1, "1 item"}, {2, "2 items"}, {0, "0 items"}} {
		got, e = c.Translate(ctx, "items", i18n.Options{Locale: "en", Params: map[string]any{"Count": test.count}, PluralCount: test.count})
		if e != nil || got.Text != test.want {
			t.Fatalf("plural %d: %+v %v", test.count, got, e)
		}
	}
	got, e = c.Translate(ctx, "welcome", i18n.Options{Locale: "fr"})
	if e != nil || got.Text != "Xin chào" || got.Locale != "vi" {
		t.Fatalf("fallback=%+v err=%v", got, e)
	}
	got, e = c.Translate(ctx, "missing", i18n.Options{Locale: "en"})
	if !errors.Is(e, i18n.ErrNotFound) || got.Text != "missing" {
		t.Fatalf("missing=%+v err=%v", got, e)
	}
	if _, e = c.Translate(ctx, "payment.received", i18n.Options{Locale: "en", Params: map[string]any{"Name": "An"}}); !errors.Is(e, i18n.ErrInvalidParams) {
		t.Fatalf("missing Amount: %v", e)
	}
	if loads != 3 {
		t.Fatalf("lookup fetched source %d times", loads)
	}
	if e = c.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Translate(ctx, "welcome", i18n.Options{}); !errors.Is(e, i18n.ErrClosed) {
		t.Fatalf("closed lookup: %v", e)
	}
}

func TestMoneyFormattingKeepsExactMinorUnits(t *testing.T) {
	for _, test := range []struct {
		locale         string
		amount         int64
		currency, want string
	}{
		{"en", 123456, "USD", "1,234.56 USD"}, {"vi", 200000, "VND", "200.000 VND"},
		{"en", -1, "USD", "-0.01 USD"}, {"en", 9223372036854775807, "USD", "92,233,720,368,547,758.07 USD"},
		{"en", -9223372036854775808, "USD", "-92,233,720,368,547,758.08 USD"}, {"en", 1234, "KWD", "1.234 KWD"},
		{"en-IN", 123456789, "INR", "12,34,567.89 INR"}, {"ar", 123456, "USD", "١٬٢٣٤٫٥٦ USD"},
	} {
		t.Run(test.want, func(t *testing.T) {
			got, e := i18n.FormatMoney(test.locale, i18n.Money{MinorUnits: test.amount, Currency: test.currency})
			if e != nil || got != test.want {
				t.Fatalf("money=%q err=%v", got, e)
			}
		})
	}
}
