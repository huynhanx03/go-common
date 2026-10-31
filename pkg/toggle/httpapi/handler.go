// Package httpapi exposes a small revision-aware HTTP management API for
// toggle flags. Authentication and scope authorization remain caller-owned.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/huynhanx03/go-common/pkg/toggle"
)

const (
	basePath       = "/v1/toggles"
	maxRequestBody = 1 << 20
)

var (
	// ErrUnauthenticated tells the handler that the request has no valid identity.
	ErrUnauthenticated = errors.New("toggle http api: unauthenticated")
	// ErrForbidden tells the handler that the identity cannot manage this scope.
	ErrForbidden = errors.New("toggle http api: forbidden")
)

// Authorizer checks whether the request identity may manage the requested
// scope. Authentication middleware should place its identity in ctx before
// this method is called. Return ErrUnauthenticated or ErrForbidden for the
// corresponding HTTP status; other errors fail closed with HTTP 500.
type Authorizer interface {
	Authorize(context.Context, toggle.Scope) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(context.Context, toggle.Scope) error

func (f AuthorizerFunc) Authorize(ctx context.Context, scope toggle.Scope) error {
	return f(ctx, scope)
}

type handler struct {
	manager    *toggle.Manager
	authorizer Authorizer
	mux        *http.ServeMux
}

// New creates the management API handler. A manager and an authorizer are
// both required so the endpoint cannot accidentally be exposed without a
// scope authorization decision.
func New(manager *toggle.Manager, authorizer Authorizer) (http.Handler, error) {
	if manager == nil || nilInterface(authorizer) {
		return nil, toggle.ErrInvalidConfig
	}
	h := &handler{manager: manager, authorizer: authorizer, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET "+basePath+"/{namespace}/{environment}", h.list)
	h.mux.HandleFunc("GET "+basePath+"/{namespace}/{environment}/{key}", h.get)
	h.mux.HandleFunc("PUT "+basePath+"/{namespace}/{environment}/{key}", h.put)
	h.mux.HandleFunc("DELETE "+basePath+"/{namespace}/{environment}/{key}", h.delete)
	return h, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.mux.ServeHTTP(w, r)
}

func (h *handler) scope(r *http.Request) (toggle.Scope, error) {
	scope := toggle.Scope{Namespace: r.PathValue("namespace"), Environment: r.PathValue("environment")}
	if err := toggle.ValidateScope(scope); err != nil {
		return toggle.Scope{}, err
	}
	if err := h.authorizer.Authorize(r.Context(), scope); err != nil {
		return toggle.Scope{}, err
	}
	return scope, nil
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	scope, err := h.scope(r)
	if err != nil {
		writeError(w, err)
		return
	}
	snapshot, err := h.manager.List(r.Context(), scope)
	if err != nil {
		writeError(w, err)
		return
	}
	setRevision(w, snapshot.Revision)
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	scope, err := h.scope(r)
	if err != nil {
		writeError(w, err)
		return
	}
	flag, revision, err := h.manager.Get(r.Context(), scope, r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	setRevision(w, revision)
	writeJSON(w, http.StatusOK, flag)
}

func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	scope, err := h.scope(r)
	if err != nil {
		writeError(w, err)
		return
	}
	expected, err := readExpectedRevision(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var flag toggle.Flag
	if err := decodeJSON(w, r, &flag); err != nil {
		writeError(w, err)
		return
	}
	if flag.Key != r.PathValue("key") {
		writeError(w, toggle.ErrInvalidDefinition)
		return
	}
	revision, err := h.manager.Put(r.Context(), scope, flag, expected)
	if err != nil {
		writeError(w, err)
		return
	}
	setRevision(w, revision)
	writeJSON(w, http.StatusOK, struct {
		Revision int64 `json:"revision"`
	}{Revision: revision})
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	scope, err := h.scope(r)
	if err != nil {
		writeError(w, err)
		return
	}
	expected, err := readExpectedRevision(r)
	if err != nil {
		writeError(w, err)
		return
	}
	revision, err := h.manager.Delete(r.Context(), scope, r.PathValue("key"), expected)
	if err != nil {
		writeError(w, err)
		return
	}
	setRevision(w, revision)
	w.WriteHeader(http.StatusNoContent)
}

type requestError struct {
	status int
	code   string
}

func (e requestError) Error() string { return e.code }

var (
	errPreconditionRequired = requestError{http.StatusPreconditionRequired, "revision_required"}
	errInvalidRevision      = requestError{http.StatusBadRequest, "invalid_revision"}
	errInvalidJSON          = requestError{http.StatusBadRequest, "invalid_json"}
	errRequestTooLarge      = requestError{http.StatusRequestEntityTooLarge, "request_too_large"}
)

func readExpectedRevision(r *http.Request) (int64, error) {
	value := r.Header.Get("If-Match")
	if value == "" {
		return 0, errPreconditionRequired
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.Contains(value[1:len(value)-1], `"`) {
		return 0, errInvalidRevision
	}
	revision, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || revision < 0 {
		return 0, errInvalidRevision
	}
	return revision, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestTooLarge
		}
		return errInvalidJSON
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestTooLarge
		}
		return errInvalidJSON
	}
	return nil
}

func setRevision(w http.ResponseWriter, revision int64) {
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, revision))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	var requestErr requestError
	switch {
	case errors.As(err, &requestErr):
		status, code = requestErr.status, requestErr.code
	case errors.Is(err, ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, toggle.ErrInvalidConfig), errors.Is(err, toggle.ErrInvalidDefinition), errors.Is(err, toggle.ErrInvalidContext), errors.Is(err, toggle.ErrLimit):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, toggle.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, toggle.ErrConflict), errors.Is(err, toggle.ErrStaleRevision):
		status, code = http.StatusPreconditionFailed, "revision_conflict"
	}
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: code})
}
