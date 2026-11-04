package gin_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	ginframework "github.com/gin-gonic/gin"
	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/i18n"
	i18ngin "github.com/huynhanx03/go-common/pkg/i18n/gin"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

type catalogStore struct {
	mu         sync.Mutex
	states     map[string]i18n.State
	readErr    error
	domainsErr error
	writeErr   error
	deleteErr  error
}

func newCatalogStore() *catalogStore { return &catalogStore{states: make(map[string]i18n.State)} }

func (s *catalogStore) Read(_ context.Context, domain string) (i18n.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return i18n.State{}, s.readErr
	}
	state, ok := s.states[domain]
	if !ok {
		return i18n.State{}, i18n.ErrNotFound
	}
	return cloneState(domain, state)
}

func (s *catalogStore) CompareAndSwap(_ context.Context, domain string, expected int64, next i18n.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	current, exists := s.states[domain]
	if (!exists && expected != 0) || (exists && current.Draft.Revision != expected) {
		return i18n.ErrConflict
	}
	owned, err := cloneState(domain, next)
	if err != nil {
		return err
	}
	s.states[domain] = owned
	return nil
}

func (s *catalogStore) Delete(_ context.Context, domain string, expected int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	current, exists := s.states[domain]
	if !exists {
		return i18n.ErrNotFound
	}
	if current.Draft.Revision != expected {
		return i18n.ErrConflict
	}
	delete(s.states, domain)
	return nil
}

func (s *catalogStore) Domains(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.domainsErr != nil {
		return nil, s.domainsErr
	}
	domains := make([]string, 0, len(s.states))
	for domain := range s.states {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains, nil
}

func cloneState(domain string, state i18n.State) (i18n.State, error) {
	return i18n.ValidateState(domain, state)
}

func newHandlerFixture(t *testing.T, store *catalogStore, authorizer i18ngin.Authorizer) (*i18n.Manager, *ginframework.Engine) {
	t.Helper()
	l1, err := ristretto.New[string, any](ristretto.WithNumCounters(10000), ristretto.WithMaxCost(8<<20))
	if err != nil {
		t.Fatal(err)
	}
	return newHandlerFixtureWithL1(t, store, authorizer, l1)
}

func newHandlerFixtureWithL1(t *testing.T, store *catalogStore, authorizer i18ngin.Authorizer, l1 cache.L1Engine) (*i18n.Manager, *ginframework.Engine) {
	t.Helper()
	ginframework.SetMode(ginframework.TestMode)
	log, err := logger.NewLogger(logger.LoggerConfig{Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	sharedCache, err := cache.New(cache.Config{L1: l1, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sharedCache.Close)
	manager, err := i18n.NewManager(store, sharedCache)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := i18ngin.New(manager, authorizer)
	if err != nil {
		t.Fatal(err)
	}
	router := ginframework.New()
	handler.RegisterRoutes(router)
	return manager, router
}

func request(router http.Handler, method, path, revision, body string) *httptest.ResponseRecorder {
	return requestWithBody(router, method, path, revision, io.NopCloser(strings.NewReader(body)))
}

func requestWithBody(router http.Handler, method, path, revision string, body io.ReadCloser) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if revision != "" {
		req.Header.Set("If-Match", revision)
	}
	req.Body = body
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestCatalogRoutesRunAFullDraftToPublishedLifecycle(t *testing.T) {
	store := newCatalogStore()
	authorizer := i18ngin.AuthorizerFunc(func(_ context.Context, domain, _ string) error {
		if domain == "private" {
			return i18ngin.ErrForbidden
		}
		if domain == "auth-error" {
			return errors.New("authorization backend unavailable")
		}
		return nil
	})
	manager, router := newHandlerFixture(t, store, authorizer)
	ctx := context.Background()
	if _, err := manager.Configure(ctx, "private", i18n.DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Configure(ctx, "visible", i18n.DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}, 0); err != nil {
		t.Fatal(err)
	}

	response := request(router, http.MethodGet, "/v1/i18n/domains", "", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("domain inventory leaked a forbidden catalog: status=%d body=%s", response.Code, response.Body)
	}
	if _, err := manager.Configure(ctx, "auth-error", i18n.DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}, 0); err != nil {
		t.Fatal(err)
	}
	if response = request(router, http.MethodGet, "/v1/i18n/domains", "", ""); response.Code != http.StatusInternalServerError {
		t.Fatalf("domain inventory authorization failure status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodGet, "/v1/i18n/domains/private", "", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("forbidden domain read status=%d body=%s", response.Code, response.Body)
	}

	response = request(router, http.MethodPut, "/v1/i18n/domains/billing", `"0"`, `{"default_locale":"vi","locales":["vi","en"]}`)
	assertRevision(t, response, 1)
	response = request(router, http.MethodGet, "/v1/i18n/domains/billing", "", "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"1"` {
		t.Fatalf("domain read status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = request(router, http.MethodPut, "/v1/i18n/domains/billing/keys/welcome", `"1"`, `{"key":"welcome","source":{"other":"Chào {{.Name}}"}}`)
	assertRevision(t, response, 2)
	response = request(router, http.MethodGet, "/v1/i18n/domains/billing/keys?limit=10&search=wel", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"welcome"`) {
		t.Fatalf("key search status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPost, "/v1/i18n/domains/billing/publish", `"2"`, "")
	if response.Code != http.StatusConflict {
		t.Fatalf("incomplete catalog publish status=%d body=%s", response.Code, response.Body)
	}
	for _, path := range []string{
		"/v1/i18n/domains/billing/catalog",
		"/v1/i18n/domains/billing/catalog/manifest",
	} {
		if response = request(router, http.MethodGet, path, "", ""); response.Code != http.StatusNotFound {
			t.Fatalf("unpublished catalog route %s status=%d body=%s", path, response.Code, response.Body)
		}
	}
	response = request(router, http.MethodPut, "/v1/i18n/domains/billing/keys/welcome/translations/en", `"2"`, `{"other":"Hello {{.Name}}"}`)
	assertRevision(t, response, 3)
	response = request(router, http.MethodPost, "/v1/i18n/domains/billing/keys/welcome/translations/en/review", `"3"`, "")
	assertRevision(t, response, 4)
	response = request(router, http.MethodGet, "/v1/i18n/domains/billing/coverage", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reviewed":1`) {
		t.Fatalf("coverage status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPost, "/v1/i18n/domains/billing/publish", `"4"`, "")
	assertRevision(t, response, 5)
	for _, path := range []string{
		"/v1/i18n/domains/billing/catalog",
		"/v1/i18n/domains/billing/catalog/manifest",
	} {
		response = request(router, http.MethodGet, path, "", "")
		if response.Code != http.StatusOK || response.Header().Get("ETag") != `"5"` {
			t.Fatalf("published route %s status=%d etag=%q body=%s", path, response.Code, response.Header().Get("ETag"), response.Body)
		}
	}
	manifest := request(router, http.MethodGet, "/v1/i18n/domains/billing/catalog/manifest", "", "")
	var info i18n.CatalogInfo
	if err := jsoncodec.Unmarshal(manifest.Body.Bytes(), &info); manifest.Code != http.StatusOK || err != nil {
		t.Fatalf("catalog manifest status=%d body=%s err=%v", manifest.Code, manifest.Body, err)
	}
	response = request(router, http.MethodGet, "/v1/i18n/domains/billing/catalog/locales/en?version="+info.Version, "", "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"5"` {
		t.Fatalf("locale catalog status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	if response = request(router, http.MethodGet, "/v1/i18n/domains/billing/catalog/locales/en?version=stale", "", ""); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale locale version status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPost, "/v1/i18n/domains/billing/sync", `"5"`, `{"keys":[{"key":"farewell","source":{"other":"Bye"}}]}`)
	assertRevision(t, response, 6)
	response = request(router, http.MethodDelete, "/v1/i18n/domains/billing/keys/welcome/translations/en", `"6"`, "")
	assertRevision(t, response, 7)
	response = request(router, http.MethodDelete, "/v1/i18n/domains/billing/keys/farewell", `"7"`, "")
	assertRevision(t, response, 8)
	if response = request(router, http.MethodDelete, "/v1/i18n/domains/billing", "", ""); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing delete precondition status=%d body=%s", response.Code, response.Body)
	}
	if response = request(router, http.MethodDelete, "/v1/i18n/domains/billing", `"8`, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed delete revision status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodDelete, "/v1/i18n/domains/billing", `"8"`, "")
	if response.Code != http.StatusNoContent || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("domain deletion status=%d cache-control=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body)
	}
}

func TestHTTPHandlerRequiresItsManagerAndAuthorizer(t *testing.T) {
	allow := i18ngin.AuthorizerFunc(func(context.Context, string, string) error { return nil })
	manager, _ := newHandlerFixture(t, newCatalogStore(), allow)
	if _, err := i18ngin.New(nil, allow); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("handler accepted missing manager: %v", err)
	}
	if _, err := i18ngin.New(manager, nil); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("handler accepted missing authorizer: %v", err)
	}
	var nilAuthorizer i18ngin.AuthorizerFunc
	if _, err := i18ngin.New(manager, nilAuthorizer); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("handler accepted typed nil authorizer: %v", err)
	}
}

func TestHTTPRevisionBodyAndAuthorizationFailuresAreMapped(t *testing.T) {
	store := newCatalogStore()
	authorizer := i18ngin.AuthorizerFunc(func(_ context.Context, domain, action string) error {
		switch {
		case domain == "failure":
			return errors.New("authorization backend unavailable")
		case domain == "private":
			return i18ngin.ErrForbidden
		case domain == "unauthenticated":
			return i18ngin.ErrUnauthenticated
		default:
			return nil
		}
	})
	_, router := newHandlerFixture(t, store, authorizer)
	if err := store.CompareAndSwap(context.Background(), "billing", 0, i18n.State{Draft: i18n.Catalog{
		Domain: "billing", Revision: 1, DomainConfig: i18n.DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}, Keys: []i18n.Key{},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, domain, revision, body string
		want                         int
	}{
		{name: "missing precondition", domain: "billing", body: `{"default_locale":"en","locales":["en"]}`, want: http.StatusPreconditionRequired},
		{name: "malformed revision", domain: "billing", revision: "1", body: `{"default_locale":"en","locales":["en"]}`, want: http.StatusBadRequest},
		{name: "negative revision", domain: "billing", revision: `"-1"`, body: `{"default_locale":"en","locales":["en"]}`, want: http.StatusBadRequest},
		{name: "malformed json", domain: "billing", revision: `"1"`, body: `{`, want: http.StatusBadRequest},
		{name: "strict json", domain: "billing", revision: `"1"`, body: `{"default_locale":"en","locales":["en"],"extra":true}`, want: http.StatusBadRequest},
		{name: "wrong route key", domain: "billing/keys/route-key", revision: `"1"`, body: `{"key":"body-key","source":{"other":"x"}}`, want: http.StatusBadRequest},
		{name: "unauthenticated", domain: "unauthenticated", revision: `"1"`, body: `{"default_locale":"en","locales":["en"]}`, want: http.StatusUnauthorized},
		{name: "forbidden", domain: "private", revision: `"0"`, body: `{"default_locale":"en","locales":["en"]}`, want: http.StatusForbidden},
		{name: "authorization backend failure", domain: "failure", revision: `"0"`, body: `{"default_locale":"en","locales":["en"]}`, want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := "/v1/i18n/domains/" + test.domain
			response := request(router, http.MethodPut, path, test.revision, test.body)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("missing no-store response policy: %q", response.Header().Get("Cache-Control"))
			}
		})
	}
	response := request(router, http.MethodGet, "/v1/i18n/domains/billing/keys?limit=invalid", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid page limit status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodGet, "/v1/i18n/domains/billing/keys?limit=201", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid page limit status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodGet, "/v1/i18n/domains/billing/keys?missing_locale=not-a-locale", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid missing-locale query status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPut, "/v1/i18n/domains/billing/keys/welcome", "1", `{"key":"welcome","source":{"other":"Welcome"}}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed key revision status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPost, "/v1/i18n/domains/billing/sync", `"1"`, `{`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed sync payload status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPut, "/v1/i18n/domains/billing", `"1"`, strings.Repeat("x", i18n.MaxCatalogBytes+1))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPut, "/v1/i18n/domains/billing/keys/welcome", `"1"`, `{`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed key body status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPost, "/v1/i18n/domains/billing/sync", "", `{"keys":[]}`)
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing sync revision status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPut, "/v1/i18n/domains/billing/keys/welcome/translations/en", `"1"`, `{`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed translation body status=%d body=%s", response.Code, response.Body)
	}
	response = request(router, http.MethodPut, "/v1/i18n/domains/billing/keys/welcome/translations/en", "", `{"other":"Hello"}`)
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing translation revision status=%d body=%s", response.Code, response.Body)
	}
	keys := make([]i18n.Definition, i18n.MaxKeys+1)
	for index := range keys {
		keys[index] = i18n.Definition{Key: fmt.Sprintf("resource-%05d", index), Source: i18n.Forms{Other: "value"}}
	}
	payload, err := jsoncodec.Marshal(struct {
		Keys []i18n.Definition `json:"keys"`
	}{Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	response = request(router, http.MethodPost, "/v1/i18n/domains/billing/sync", `"1"`, string(payload))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("catalog key limit status=%d body=%s", response.Code, response.Body)
	}
	response = requestWithBody(router, http.MethodPut, "/v1/i18n/domains/billing", `"1"`, failingBody{})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unreadable request body status=%d body=%s", response.Code, response.Body)
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("request stream failed") }
func (failingBody) Close() error             { return nil }

func TestHTTPMapsReadAndWriteStoreFailuresAtTheRouteBoundary(t *testing.T) {
	store := newCatalogStore()
	manager, router := newHandlerFixture(t, store, i18ngin.AuthorizerFunc(func(context.Context, string, string) error { return nil }))
	revision, err := manager.Configure(context.Background(), "billing", i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.PutKey(context.Background(), "billing", i18n.Definition{Key: "welcome", Source: i18n.Forms{Other: "Welcome"}}, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.PutTranslation(context.Background(), "billing", "welcome", "en", i18n.Forms{Other: "Welcome"}, revision)
	if err != nil || revision != 3 {
		t.Fatalf("prepare translation revision=%d err=%v", revision, err)
	}
	store.domainsErr = errors.New("inventory unavailable")
	if response := request(router, http.MethodGet, "/v1/i18n/domains", "", ""); response.Code != http.StatusInternalServerError {
		t.Fatalf("domain inventory storage failure status=%d body=%s", response.Code, response.Body)
	}
	store.domainsErr = nil
	store.readErr = errors.New("catalog read unavailable")
	for _, path := range []string{
		"/v1/i18n/domains/billing",
		"/v1/i18n/domains/billing/keys",
		"/v1/i18n/domains/billing/coverage",
		"/v1/i18n/domains/billing/catalog",
		"/v1/i18n/domains/billing/catalog/manifest",
		"/v1/i18n/domains/billing/catalog/locales/en",
	} {
		response := request(router, http.MethodGet, path, "", "")
		if response.Code != http.StatusInternalServerError {
			t.Errorf("read route %s leaked a storage failure: status=%d body=%s", path, response.Code, response.Body)
		}
	}
	store.readErr = nil
	store.writeErr = errors.New("catalog write unavailable")
	mutations := []struct{ method, path, body string }{
		{http.MethodPut, "/v1/i18n/domains/billing", `{"default_locale":"vi","locales":["vi","en","fr"]}`},
		{http.MethodPut, "/v1/i18n/domains/billing/keys/welcome", `{"key":"welcome","source":{"other":"Greeting"}}`},
		{http.MethodPost, "/v1/i18n/domains/billing/sync", `{"keys":[{"key":"farewell","source":{"other":"Bye"}}]}`},
		{http.MethodPut, "/v1/i18n/domains/billing/keys/welcome/translations/en", `{"other":"Hello"}`},
		{http.MethodPost, "/v1/i18n/domains/billing/keys/welcome/translations/en/review", ""},
		{http.MethodDelete, "/v1/i18n/domains/billing/keys/welcome/translations/en", ""},
		{http.MethodDelete, "/v1/i18n/domains/billing/keys/welcome", ""},
	}
	for _, mutation := range mutations {
		response := request(router, mutation.method, mutation.path, `"3"`, mutation.body)
		if response.Code != http.StatusInternalServerError {
			t.Errorf("write route %s %s status=%d body=%s", mutation.method, mutation.path, response.Code, response.Body)
		}
	}
	store.writeErr = nil
	store.deleteErr = errors.New("catalog delete unavailable")
	if response := request(router, http.MethodDelete, "/v1/i18n/domains/billing", `"3"`, ""); response.Code != http.StatusInternalServerError {
		t.Fatalf("domain delete storage failure status=%d body=%s", response.Code, response.Body)
	}
}

type failingRevokeL1 struct {
	cache.L1Engine
	fail bool
}

func (l1 *failingRevokeL1) Revoke(ctx context.Context, key string) error {
	if l1.fail {
		return errors.New("cache invalidation unavailable")
	}
	return l1.L1Engine.Revoke(ctx, key)
}

func TestPublishReportsTheRevisionWhenCacheInvalidationFailsAfterCommit(t *testing.T) {
	inner, err := ristretto.New[string, any](ristretto.WithNumCounters(10000), ristretto.WithMaxCost(8<<20))
	if err != nil {
		t.Fatal(err)
	}
	l1 := &failingRevokeL1{L1Engine: inner}
	manager, router := newHandlerFixtureWithL1(t, newCatalogStore(), i18ngin.AuthorizerFunc(func(context.Context, string, string) error { return nil }), l1)
	ctx := context.Background()
	revision, err := manager.Configure(ctx, "billing", i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.PutKey(ctx, "billing", i18n.Definition{Key: "welcome", Source: i18n.Forms{Other: "Xin chào"}}, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.PutTranslation(ctx, "billing", "welcome", "en", i18n.Forms{Other: "Hello"}, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = manager.Review(ctx, "billing", "welcome", "en", revision)
	if err != nil {
		t.Fatal(err)
	}
	l1.fail = true
	response := request(router, http.MethodPost, "/v1/i18n/domains/billing/publish", fmt.Sprintf(`"%d"`, revision), "")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("ETag") != fmt.Sprintf(`"%d"`, revision+1) {
		t.Fatalf("post-commit invalidation failure status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	state, err := manager.Get(ctx, "billing")
	if err != nil || state.Published == nil || state.Published.Revision != revision+1 {
		t.Fatalf("published state was not committed: state=%+v err=%v", state, err)
	}
}

type structAuthorizer struct{}

func (structAuthorizer) Authorize(context.Context, string, string) error { return nil }

func TestHandlerAcceptsValueAuthorizersAndRejectsInvalidDomainPaths(t *testing.T) {
	manager, router := newHandlerFixture(t, newCatalogStore(), i18ngin.AuthorizerFunc(func(_ context.Context, domain, _ string) error {
		if domain == "auth-error" {
			return errors.New("authorization backend unavailable")
		}
		return nil
	}))
	if _, err := i18ngin.New(manager, structAuthorizer{}); err != nil {
		t.Fatalf("value authorizer rejected: %v", err)
	}
	if response := request(router, http.MethodGet, "/v1/i18n/domains/auth-error", "", ""); response.Code != http.StatusInternalServerError {
		t.Fatalf("authorization backend error status=%d body=%s", response.Code, response.Body)
	}
	if response := request(router, http.MethodGet, "/v1/i18n/domains/bad%20domain", "", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid domain path status=%d body=%s", response.Code, response.Body)
	}
}

func TestEveryDomainScopedRouteRequiresItsPermission(t *testing.T) {
	_, router := newHandlerFixture(t, newCatalogStore(), i18ngin.AuthorizerFunc(func(context.Context, string, string) error {
		return i18ngin.ErrForbidden
	}))
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/i18n/domains/private", ""},
		{http.MethodPut, "/v1/i18n/domains/private", `{"default_locale":"en","locales":["en"]}`},
		{http.MethodDelete, "/v1/i18n/domains/private", ""},
		{http.MethodGet, "/v1/i18n/domains/private/keys", ""},
		{http.MethodPut, "/v1/i18n/domains/private/keys/welcome", `{"key":"welcome","source":{"other":"Welcome"}}`},
		{http.MethodDelete, "/v1/i18n/domains/private/keys/welcome", ""},
		{http.MethodPost, "/v1/i18n/domains/private/sync", `{"keys":[]}`},
		{http.MethodPut, "/v1/i18n/domains/private/keys/welcome/translations/en", `{"other":"Welcome"}`},
		{http.MethodDelete, "/v1/i18n/domains/private/keys/welcome/translations/en", ""},
		{http.MethodPost, "/v1/i18n/domains/private/keys/welcome/translations/en/review", ""},
		{http.MethodGet, "/v1/i18n/domains/private/coverage", ""},
		{http.MethodPost, "/v1/i18n/domains/private/publish", ""},
		{http.MethodGet, "/v1/i18n/domains/private/catalog", ""},
		{http.MethodGet, "/v1/i18n/domains/private/catalog/manifest", ""},
		{http.MethodGet, "/v1/i18n/domains/private/catalog/locales/en", ""},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := request(router, test.method, test.path, `"0"`, test.body)
			if response.Code != http.StatusForbidden {
				t.Fatalf("unauthorized route status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

type authorizationStub struct {
	decision authorization.Decision
	err      error
	request  authorization.Request
	calls    int
}

func (s *authorizationStub) Authorize(_ context.Context, request authorization.Request) (authorization.Decision, error) {
	s.calls++
	s.request = request
	return s.decision, s.err
}

func TestSecurityAuthorizerRequiresAnAuthenticatedPrincipalAndScopesTheDecision(t *testing.T) {
	if _, err := i18ngin.NewSecurityAuthorizer(nil); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("security adapter accepted missing authorizer: %v", err)
	}
	var nilStub *authorizationStub
	if _, err := i18ngin.NewSecurityAuthorizer(nilStub); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("security adapter accepted typed nil authorizer: %v", err)
	}
	stub := &authorizationStub{decision: authorization.Decision{Allowed: true}}
	adapter, err := i18ngin.NewSecurityAuthorizer(stub)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Authorize(nil, "billing", i18ngin.ActionCatalogRead); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil request context error=%v", err)
	}
	if err := adapter.Authorize(context.Background(), "bad domain", i18ngin.ActionCatalogRead); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("invalid domain error=%v", err)
	}
	if err := adapter.Authorize(context.Background(), "billing", i18ngin.ActionCatalogRead); !errors.Is(err, i18ngin.ErrUnauthenticated) {
		t.Fatalf("missing principal error=%v", err)
	}

	ctx := authentication.WithPrincipal(context.Background(), authentication.Principal{Authenticated: true, Subject: "user-1", AuthenticatedUntil: time.Now().Add(time.Hour)})
	if err := adapter.Authorize(ctx, "billing", i18ngin.ActionCatalogRead); err != nil {
		t.Fatalf("authorized principal rejected: %v", err)
	}
	if stub.request.Scope != (authorization.Scope{Type: "i18n", ID: "billing"}) || stub.request.Action != i18ngin.ActionCatalogRead || stub.request.ResourceID != "billing" {
		t.Fatalf("authorization decision used wrong scope: %+v", stub.request)
	}

	stub.decision.Allowed = false
	if err := adapter.Authorize(ctx, "billing", i18ngin.ActionCatalogRead); !errors.Is(err, i18ngin.ErrForbidden) {
		t.Fatalf("denied decision error=%v", err)
	}
	backendFailure := errors.New("authorization backend unavailable")
	stub.err = backendFailure
	if err := adapter.Authorize(ctx, "billing", i18ngin.ActionCatalogRead); !errors.Is(err, backendFailure) {
		t.Fatalf("authorization backend error=%v", err)
	}
	var nilAdapter *i18ngin.SecurityAuthorizer
	if err := nilAdapter.Authorize(ctx, "billing", i18ngin.ActionCatalogRead); !errors.Is(err, i18n.ErrInvalidConfig) {
		t.Fatalf("nil security adapter error=%v", err)
	}
}

func assertRevision(t *testing.T, response *httptest.ResponseRecorder, revision int64) {
	t.Helper()
	want := fmt.Sprintf("%q", fmt.Sprint(revision))
	if response.Code != http.StatusOK || response.Header().Get("ETag") != want {
		t.Fatalf("revision response status=%d etag=%q want=%q body=%s", response.Code, response.Header().Get("ETag"), want, response.Body)
	}
	var body map[string]any
	if err := jsoncodec.Unmarshal(response.Body.Bytes(), &body); err != nil || body["revision"] != float64(revision) {
		t.Fatalf("revision response body=%s err=%v", response.Body, err)
	}
}
