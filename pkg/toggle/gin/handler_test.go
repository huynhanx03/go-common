package gin_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huynhanx03/go-common/pkg/toggle"
	togglegin "github.com/huynhanx03/go-common/pkg/toggle/gin"
)

type apiStore struct {
	mu       sync.Mutex
	snapshot toggle.Snapshot
	loads    int
}

func (s *apiStore) Load(ctx context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return toggle.Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	return s.snapshot, nil
}

func (s *apiStore) Put(ctx context.Context, scope toggle.Scope, flag toggle.Flag, expected int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Scope != scope || s.snapshot.Revision != expected {
		return 0, toggle.ErrConflict
	}
	s.snapshot.Revision++
	for i := range s.snapshot.Flags {
		if s.snapshot.Flags[i].Key == flag.Key {
			s.snapshot.Flags[i] = flag
			return s.snapshot.Revision, nil
		}
	}
	s.snapshot.Flags = append(s.snapshot.Flags, flag)
	return s.snapshot.Revision, nil
}

func (s *apiStore) Delete(ctx context.Context, scope toggle.Scope, key string, expected int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Scope != scope || s.snapshot.Revision != expected {
		return 0, toggle.ErrConflict
	}
	for i := range s.snapshot.Flags {
		if s.snapshot.Flags[i].Key == key {
			s.snapshot.Flags = append(s.snapshot.Flags[:i], s.snapshot.Flags[i+1:]...)
			s.snapshot.Revision++
			return s.snapshot.Revision, nil
		}
	}
	return 0, toggle.ErrNotFound
}

func newGinHandler(t *testing.T, store *apiStore, authorize togglegin.Authorizer) *gin.Engine {
	t.Helper()
	manager, err := toggle.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := togglegin.New(manager, authorize)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handler.RegisterRoutes(router.Group("/admin"))
	return router
}

func TestGinManagementAPIProvidesScopedRevisionAwareCRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	scope := toggle.Scope{Namespace: "billing", Environment: "production"}
	store := &apiStore{snapshot: toggle.Snapshot{Scope: scope}}
	router := newGinHandler(t, store, togglegin.AuthorizerFunc(func(_ context.Context, requested toggle.Scope) error {
		if requested != scope {
			return togglegin.ErrForbidden
		}
		return nil
	}))
	path := "/admin/v1/toggles/billing/production"

	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, path, nil))
	if list.Code != http.StatusOK || list.Header().Get("ETag") != `"0"` || list.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list = status %d etag %q cache-control %q body %q", list.Code, list.Header().Get("ETag"), list.Header().Get("Cache-Control"), list.Body.String())
	}

	put := httptest.NewRecorder()
	putRequest := httptest.NewRequest(http.MethodPut, path+"/checkout-v2", bytes.NewBufferString(`{"key":"checkout-v2","enabled":true,"description":"new flow"}`))
	putRequest.Header.Set("If-Match", `"0"`)
	router.ServeHTTP(put, putRequest)
	if put.Code != http.StatusOK || put.Header().Get("ETag") != `"1"` || !bytes.Contains(put.Body.Bytes(), []byte(`"revision":1`)) {
		t.Fatalf("put = status %d etag %q body %q", put.Code, put.Header().Get("ETag"), put.Body.String())
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path+"/checkout-v2", nil))
	if get.Code != http.StatusOK || get.Header().Get("ETag") != `"1"` || !bytes.Contains(get.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("get = status %d etag %q body %q", get.Code, get.Header().Get("ETag"), get.Body.String())
	}

	remove := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, path+"/checkout-v2", nil)
	deleteRequest.Header.Set("If-Match", `"1"`)
	router.ServeHTTP(remove, deleteRequest)
	if remove.Code != http.StatusNoContent || remove.Header().Get("ETag") != `"2"` {
		t.Fatalf("delete = status %d etag %q body %q", remove.Code, remove.Header().Get("ETag"), remove.Body.String())
	}
}

func TestGinManagementAPIAuthorizesBeforeReadingStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	scope := toggle.Scope{Namespace: "billing", Environment: "production"}
	store := &apiStore{snapshot: toggle.Snapshot{Scope: scope}}
	router := newGinHandler(t, store, togglegin.AuthorizerFunc(func(context.Context, toggle.Scope) error {
		return togglegin.ErrForbidden
	}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/v1/toggles/billing/production", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	store.mu.Lock()
	loads := store.loads
	store.mu.Unlock()
	if loads != 0 {
		t.Fatalf("unauthorized request loaded store %d times", loads)
	}
}

func TestGinManagementAPIMapsUnauthenticatedAndMissingFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	scope := toggle.Scope{Namespace: "billing", Environment: "production"}
	store := &apiStore{snapshot: toggle.Snapshot{Scope: scope}}

	unauthenticated := newGinHandler(t, store, togglegin.AuthorizerFunc(func(context.Context, toggle.Scope) error {
		return togglegin.ErrUnauthenticated
	}))
	unauthenticatedResponse := httptest.NewRecorder()
	unauthenticated.ServeHTTP(unauthenticatedResponse, httptest.NewRequest(http.MethodGet, "/admin/v1/toggles/billing/production", nil))
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticatedResponse.Code, http.StatusUnauthorized)
	}

	authorized := newGinHandler(t, store, togglegin.AuthorizerFunc(func(context.Context, toggle.Scope) error { return nil }))
	missingResponse := httptest.NewRecorder()
	authorized.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, "/admin/v1/toggles/billing/production/missing", nil))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing flag status = %d, want %d", missingResponse.Code, http.StatusNotFound)
	}
}

func TestGinManagementAPIRejectsUnconditionalStaleAndNonStrictWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	scope := toggle.Scope{Namespace: "billing", Environment: "production"}
	store := &apiStore{snapshot: toggle.Snapshot{Scope: scope, Revision: 4}}
	router := newGinHandler(t, store, togglegin.AuthorizerFunc(func(context.Context, toggle.Scope) error { return nil }))
	path := "/admin/v1/toggles/billing/production/checkout-v2"

	for _, test := range []struct {
		name, body, revision string
		want                 int
	}{
		{name: "missing revision", body: `{"key":"checkout-v2","enabled":true}`, want: http.StatusPreconditionRequired},
		{name: "stale revision", body: `{"key":"checkout-v2","enabled":true}`, revision: `"3"`, want: http.StatusPreconditionFailed},
		{name: "path key mismatch", body: `{"key":"other","enabled":true}`, revision: `"4"`, want: http.StatusBadRequest},
		{name: "unknown field", body: `{"key":"checkout-v2","enabled":true,"unexpected":true}`, revision: `"4"`, want: http.StatusBadRequest},
		{name: "duplicate field", body: `{"key":"checkout-v2","enabled":true,"enabled":false}`, revision: `"4"`, want: http.StatusBadRequest},
		{name: "oversized body", body: `{"key":"checkout-v2","description":"` + strings.Repeat("x", 1<<20) + `"}`, revision: `"4"`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(test.body))
			if test.revision != "" {
				request.Header.Set("If-Match", test.revision)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body %q", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestGinManagementAPIHidesAuthorizationBackendErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &apiStore{snapshot: toggle.Snapshot{Scope: toggle.Scope{Namespace: "billing", Environment: "production"}}}
	router := newGinHandler(t, store, togglegin.AuthorizerFunc(func(context.Context, toggle.Scope) error {
		return errors.New("authorization service unavailable")
	}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/v1/toggles/billing/production", nil))
	if response.Code != http.StatusInternalServerError || bytes.Contains(response.Body.Bytes(), []byte("authorization service unavailable")) {
		t.Fatalf("authorization error = status %d body %q", response.Code, response.Body.String())
	}
}
