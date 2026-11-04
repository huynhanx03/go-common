package i18n_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
	"github.com/huynhanx03/go-common/pkg/i18n"
	i18ngin "github.com/huynhanx03/go-common/pkg/i18n/gin"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func apiRouter(t *testing.T, manager *i18n.Manager) *gin.Engine {
	t.Helper()
	authorizer := i18ngin.AuthorizerFunc(func(_ context.Context, domain, _ string) error {
		if domain == "private" {
			return i18ngin.ErrForbidden
		}
		return nil
	})
	handler, err := i18ngin.New(manager, authorizer)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handler.RegisterRoutes(router)
	return router
}

func apiRequest(router http.Handler, method, path, etag, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type localHTTPTransport struct {
	handler http.Handler
	mu      sync.Mutex
	paths   []string
}

func (transport *localHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.paths = append(transport.paths, request.URL.RequestURI())
	transport.mu.Unlock()
	response := httptest.NewRecorder()
	transport.handler.ServeHTTP(response, request)
	return response.Result(), nil
}

// Drives each permission and revision boundary through the public HTTP API.
func TestManagementAPIEnforcesDomainAccessAndPublishLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m, _ := manager(t)
	if _, err := m.Configure(ctx, "private", i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, 0); err != nil {
		t.Fatal(err)
	}
	router := apiRouter(t, m)

	response := apiRequest(router, http.MethodPut, "/v1/i18n/domains/private", `"0"`, `{"default_locale":"vi","locales":["vi","en"]}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized domain write status=%d body=%s", response.Code, response.Body)
	}
	response = apiRequest(router, http.MethodGet, "/v1/i18n/domains", "", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("domain listing leaked unauthorized domain: status=%d body=%s", response.Code, response.Body)
	}

	response = apiRequest(router, http.MethodPut, "/v1/i18n/domains/billing", `"0"`, `{"default_locale":"vi","locales":["vi","en"]}`)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"1"` {
		t.Fatalf("configure status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodPut, "/v1/i18n/domains/billing/keys/payment.ok", `"1"`, `{"key":"payment.ok","source":{"other":"Đã nhận {{.Amount}}"}}`)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"2"` {
		t.Fatalf("put source status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodPost, "/v1/i18n/domains/billing/publish", `"2"`, "")
	if response.Code != http.StatusConflict {
		t.Fatalf("publish incomplete status=%d body=%s", response.Code, response.Body)
	}
	response = apiRequest(router, http.MethodPut, "/v1/i18n/domains/billing/keys/payment.ok/translations/en", `"2"`, `{"other":"Received {{.Amount}}"}`)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"3"` {
		t.Fatalf("put translation status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodPost, "/v1/i18n/domains/billing/keys/payment.ok/translations/en/review", `"3"`, "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"4"` {
		t.Fatalf("review status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodPost, "/v1/i18n/domains/billing/publish", `"4"`, "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"5"` {
		t.Fatalf("publish status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodGet, "/v1/i18n/domains/billing/catalog", "", "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"5"` || !strings.Contains(response.Body.String(), "Received") {
		t.Fatalf("catalog status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodDelete, "/v1/i18n/domains/billing/keys/payment.ok", `"3"`, "")
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale write status=%d body=%s", response.Code, response.Body)
	}
	response = apiRequest(router, http.MethodPut, "/v1/i18n/domains/billing", `"5"`, `{"default_locale":"vi","locales":["vi","en"],"admin":true}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON member status=%d body=%s", response.Code, response.Body)
	}
	response = apiRequest(router, http.MethodDelete, "/v1/i18n/domains/billing/keys/payment.ok/translations/en", `"5"`, "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"6"` {
		t.Fatalf("delete translation status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodDelete, "/v1/i18n/domains/billing/keys/payment.ok", `"6"`, "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"7"` {
		t.Fatalf("delete key status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	response = apiRequest(router, http.MethodDelete, "/v1/i18n/domains/billing", `"7"`, "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete domain status=%d body=%s", response.Code, response.Body)
	}
	response = apiRequest(router, http.MethodGet, "/v1/i18n/domains/billing", "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("deleted domain status=%d body=%s", response.Code, response.Body)
	}
}

func TestManagementAPIReportsCorruptPersistedCatalogAsInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m, store := manager(t)
	store.states["billing"] = i18n.State{Draft: i18n.Catalog{
		Domain: "billing", Revision: 1,
		DomainConfig: i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi"}},
		Keys:         []i18n.Key{{Definition: i18n.Definition{Key: "welcome", Source: i18n.Forms{Other: "bad\x00text"}}}},
	}}

	response := apiRequest(apiRouter(t, m), http.MethodGet, "/v1/i18n/domains/billing/catalog/manifest", "", "")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"error":"internal_error"`) {
		t.Fatalf("corrupt source status=%d body=%s", response.Code, response.Body)
	}
}

// Exercises management HTTP, domain authorization, HTTPSource, and local lookup
// together so a service can consume the exact published catalog the API serves.
func TestHTTPSourceLoadsPublishedCatalogForLocalClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m, _ := manager(t)
	published(t, m, "billing")
	router := apiRouter(t, m)
	transport := &localHTTPTransport{handler: router}
	httpClient := &http.Client{Transport: transport}
	source, err := i18n.NewHTTPSource("http://i18n.local", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	client, err := i18n.New(ctx, settings.I18n{Domain: "billing", CacheTTLSeconds: 3600}, i18n.ClientConfig{Source: source, Cache: sharedCache(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	got, err := client.Translate(ctx, "welcome", i18n.Options{Locale: "en"})
	if err != nil || got.Text != "Hello" {
		t.Fatalf("local client result=%+v err=%v", got, err)
	}
	transport.mu.Lock()
	paths := append([]string(nil), transport.paths...)
	transport.mu.Unlock()
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/catalog/manifest") || !strings.Contains(paths[1], "/catalog/locales/en?version=") {
		t.Fatalf("runtime did not request only the manifest and selected locale: %v", paths)
	}
	if _, err := source.Info(ctx, "private"); !errors.Is(err, i18n.ErrSource) {
		t.Fatalf("unauthorized source load error=%v", err)
	}
}

func TestAPIReportsCommittedRevisionWhenCacheInvalidationFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
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

	response := apiRequest(apiRouter(t, manager), http.MethodPost, "/v1/i18n/domains/billing/publish", `"`+strconv.FormatInt(revision, 10)+`"`, "")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("ETag") != `"`+strconv.FormatInt(revision+1, 10)+`"` {
		t.Fatalf("post-commit failure status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	if catalog, err := manager.Load(ctx, "billing"); err != nil || catalog.Revision != revision+1 {
		t.Fatalf("API failure hid committed catalog: catalog=%+v err=%v", catalog, err)
	}
	committedRevision := revision + 1
	response = apiRequest(apiRouter(t, manager), http.MethodDelete, "/v1/i18n/domains/billing", `"`+strconv.FormatInt(committedRevision, 10)+`"`, "")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("ETag") != `"`+strconv.FormatInt(committedRevision, 10)+`"` {
		t.Fatalf("post-commit delete failure status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body)
	}
	l1.fail.Store(false)
	if _, err := manager.Get(ctx, "billing"); !errors.Is(err, i18n.ErrNotFound) {
		t.Fatalf("API failure hid committed delete: %v", err)
	}
}

type authorizationFunc func(context.Context, authorization.Request) (authorization.Decision, error)

func (f authorizationFunc) Authorize(ctx context.Context, request authorization.Request) (authorization.Decision, error) {
	return f(ctx, request)
}

func TestSecurityAdapterPassesDomainAndActionFromAuthenticatedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m, _ := manager(t)
	published(t, m, "billing")
	var got authorization.Request
	adapter, err := i18ngin.NewSecurityAuthorizer(authorizationFunc(func(_ context.Context, request authorization.Request) (authorization.Decision, error) {
		got = request
		return authorization.Decision{Allowed: request.Action == i18ngin.ActionCatalogRead}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := i18ngin.New(m, adapter)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handler.RegisterRoutes(router)
	principal := authentication.Principal{Authenticated: true, Subject: "user-1", AuthenticatedUntil: time.Now().Add(time.Hour)}
	request := httptest.NewRequest(http.MethodGet, "/v1/i18n/domains/billing/catalog", nil)
	request = request.WithContext(authentication.WithPrincipal(request.Context(), principal))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || got.Scope != (authorization.Scope{Type: "i18n", ID: "billing"}) || got.Action != i18ngin.ActionCatalogRead || got.ResourceID != "billing" {
		t.Fatalf("authorized request status=%d scope=%+v action=%q resource=%q body=%s", response.Code, got.Scope, got.Action, got.ResourceID, response.Body)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/i18n/domains/billing/coverage", nil)
	request = request.WithContext(authentication.WithPrincipal(request.Context(), principal))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || got.Action != "domain:read" {
		t.Fatalf("ungranted action status=%d action=%q body=%s", response.Code, got.Action, response.Body)
	}
	response = apiRequest(router, http.MethodGet, "/v1/i18n/domains/billing/catalog", "", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing principal status=%d body=%s", response.Code, response.Body)
	}
}
