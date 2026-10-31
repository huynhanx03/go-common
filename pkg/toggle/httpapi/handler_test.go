package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/httpapi"
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
	if s.snapshot.Scope == (toggle.Scope{}) {
		s.snapshot.Scope = scope
	}
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

func newAPIHandler(t *testing.T, store *apiStore, authorize httpapi.Authorizer) http.Handler {
	t.Helper()
	manager, err := toggle.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(manager, authorize)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHandlerManagesFlagsWithScopeAuthorizationAndRevisionETags(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "production"}
	store := &apiStore{snapshot: toggle.Snapshot{Scope: scope}}
	handler := newAPIHandler(t, store, httpapi.AuthorizerFunc(func(ctx context.Context, requested toggle.Scope) error {
		if requested != scope {
			return httpapi.ErrForbidden
		}
		return nil
	}))

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/toggles/billing/production", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || listResponse.Header().Get("ETag") != `"0"` || listResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("initial list = status %d etag %q cache-control %q body %q", listResponse.Code, listResponse.Header().Get("ETag"), listResponse.Header().Get("Cache-Control"), listResponse.Body.String())
	}

	putRequest := httptest.NewRequest(http.MethodPut, "/v1/toggles/billing/production/checkout-v2", bytes.NewBufferString(`{"key":"checkout-v2","enabled":true}`))
	putRequest.Header.Set("If-Match", `"0"`)
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, putRequest)
	if putResponse.Code != http.StatusOK || putResponse.Header().Get("ETag") != `"1"` {
		t.Fatalf("put = status %d etag %q body %q", putResponse.Code, putResponse.Header().Get("ETag"), putResponse.Body.String())
	}
	var putResult struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(putResponse.Body.Bytes(), &putResult); err != nil || putResult.Revision != 1 {
		t.Fatalf("put response = %+v, error %v", putResult, err)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/toggles/billing/production/checkout-v2", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	var got toggle.Flag
	if err := json.Unmarshal(getResponse.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if getResponse.Code != http.StatusOK || getResponse.Header().Get("ETag") != `"1"` || got.Key != "checkout-v2" || !got.Enabled {
		t.Fatalf("get = status %d etag %q flag %+v", getResponse.Code, getResponse.Header().Get("ETag"), got)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/toggles/billing/production/checkout-v2", nil)
	deleteRequest.Header.Set("If-Match", `"1"`)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent || deleteResponse.Header().Get("ETag") != `"2"` {
		t.Fatalf("delete = status %d etag %q body %q", deleteResponse.Code, deleteResponse.Header().Get("ETag"), deleteResponse.Body.String())
	}
}

func TestHandlerRejectsUnauthorizedScopeBeforeReadingStore(t *testing.T) {
	store := &apiStore{snapshot: toggle.Snapshot{Scope: toggle.Scope{Namespace: "billing", Environment: "production"}}}
	handler := newAPIHandler(t, store, httpapi.AuthorizerFunc(func(context.Context, toggle.Scope) error {
		return httpapi.ErrForbidden
	}))

	request := httptest.NewRequest(http.MethodGet, "/v1/toggles/billing/production", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	store.mu.Lock()
	loads := store.loads
	store.mu.Unlock()
	if loads != 0 {
		t.Fatalf("unauthorized request read store %d times", loads)
	}
}

func TestHandlerRequiresRevisionAndReturnsConflictForStaleWrites(t *testing.T) {
	scope := toggle.Scope{Namespace: "billing", Environment: "production"}
	store := &apiStore{snapshot: toggle.Snapshot{Scope: scope, Revision: 4}}
	handler := newAPIHandler(t, store, httpapi.AuthorizerFunc(func(context.Context, toggle.Scope) error { return nil }))
	body := `{"key":"checkout-v2","enabled":true}`

	missingRevision := httptest.NewRequest(http.MethodPut, "/v1/toggles/billing/production/checkout-v2", bytes.NewBufferString(body))
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingRevision)
	if missingResponse.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing revision status = %d, want %d", missingResponse.Code, http.StatusPreconditionRequired)
	}

	staleRevision := httptest.NewRequest(http.MethodPut, "/v1/toggles/billing/production/checkout-v2", bytes.NewBufferString(body))
	staleRevision.Header.Set("If-Match", `"3"`)
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, staleRevision)
	if staleResponse.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale revision status = %d, want %d; body %q", staleResponse.Code, http.StatusPreconditionFailed, staleResponse.Body.String())
	}
}

func TestHandlerMapsAuthorizationAndInputErrorsToSafeStatuses(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		store := &apiStore{snapshot: toggle.Snapshot{Scope: toggle.Scope{Namespace: "billing", Environment: "production"}}}
		handler := newAPIHandler(t, store, httpapi.AuthorizerFunc(func(context.Context, toggle.Scope) error {
			return httpapi.ErrUnauthenticated
		}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/toggles/billing/production", nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
	})

	t.Run("invalid definition", func(t *testing.T) {
		scope := toggle.Scope{Namespace: "billing", Environment: "production"}
		store := &apiStore{snapshot: toggle.Snapshot{Scope: scope}}
		handler := newAPIHandler(t, store, httpapi.AuthorizerFunc(func(context.Context, toggle.Scope) error { return nil }))
		request := httptest.NewRequest(http.MethodPut, "/v1/toggles/billing/production/checkout-v2", bytes.NewBufferString(`{"key":"checkout-v2","enabled":true,"surprise":1}`))
		request.Header.Set("If-Match", `"0"`)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
		}
	})

	t.Run("authorization backend failure", func(t *testing.T) {
		store := &apiStore{snapshot: toggle.Snapshot{Scope: toggle.Scope{Namespace: "billing", Environment: "production"}}}
		handler := newAPIHandler(t, store, httpapi.AuthorizerFunc(func(context.Context, toggle.Scope) error {
			return errors.New("authorization service unavailable")
		}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/toggles/billing/production", nil))
		if response.Code != http.StatusInternalServerError || bytes.Contains(response.Body.Bytes(), []byte("authorization service unavailable")) {
			t.Fatalf("authorization error response = status %d body %q", response.Code, response.Body.String())
		}
	})
}
