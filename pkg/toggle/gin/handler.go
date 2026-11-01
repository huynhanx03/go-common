// Package gin exposes Gin routes for managing toggle definitions.
package gin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	ginframework "github.com/gin-gonic/gin"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/toggle"
)

const (
	basePath       = "/v1/toggles"
	maxRequestBody = 1 << 20
)

var (
	// ErrUnauthenticated tells the handler that the request has no valid identity.
	ErrUnauthenticated = errors.New("toggle gin: unauthenticated")
	// ErrForbidden tells the handler that the identity cannot manage the scope.
	ErrForbidden = errors.New("toggle gin: forbidden")
)

// Authorizer checks whether the request identity may manage a scope. The
// service's authentication middleware should place its identity in the
// request context before the handler runs.
type Authorizer interface {
	Authorize(context.Context, toggle.Scope) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(context.Context, toggle.Scope) error

func (f AuthorizerFunc) Authorize(ctx context.Context, scope toggle.Scope) error {
	return f(ctx, scope)
}

// Handler serves the toggle management API through routes registered on a
// caller-owned Gin router or route group.
type Handler struct {
	manager    *toggle.Manager
	authorizer Authorizer
}

// New creates a management handler. A manager and authorizer are required so
// every route has a service-owned scope authorization decision.
func New(manager *toggle.Manager, authorizer Authorizer) (*Handler, error) {
	if manager == nil || nilInterface(authorizer) {
		return nil, toggle.ErrInvalidConfig
	}
	return &Handler{manager: manager, authorizer: authorizer}, nil
}

// RegisterRoutes registers CRUD routes relative to routes. Callers can pass a
// Gin group with their normal authentication and network middleware attached.
func (h *Handler) RegisterRoutes(routes ginframework.IRoutes) {
	routes.Handle(http.MethodGet, basePath+"/:namespace/:environment", h.noStore(h.list))
	routes.Handle(http.MethodGet, basePath+"/:namespace/:environment/:key", h.noStore(h.get))
	routes.Handle(http.MethodPut, basePath+"/:namespace/:environment/:key", h.noStore(h.put))
	routes.Handle(http.MethodDelete, basePath+"/:namespace/:environment/:key", h.noStore(h.delete))
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

func (h *Handler) noStore(next ginframework.HandlerFunc) ginframework.HandlerFunc {
	return func(c *ginframework.Context) {
		c.Header("Cache-Control", "no-store")
		next(c)
	}
}

func (h *Handler) scope(c *ginframework.Context) (toggle.Scope, error) {
	scope := toggle.Scope{Namespace: c.Param("namespace"), Environment: c.Param("environment")}
	if err := toggle.ValidateScope(scope); err != nil {
		return toggle.Scope{}, err
	}
	if err := h.authorizer.Authorize(c.Request.Context(), scope); err != nil {
		return toggle.Scope{}, err
	}
	return scope, nil
}

func (h *Handler) list(c *ginframework.Context) {
	scope, err := h.scope(c)
	if err != nil {
		writeError(c, err)
		return
	}
	snapshot, err := h.manager.List(c.Request.Context(), scope)
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, snapshot.Revision)
	flags := make([]flagResponse, len(snapshot.Flags))
	for i, flag := range snapshot.Flags {
		flags[i] = responseFlag(flag)
	}
	writeJSON(c, http.StatusOK, snapshotResponse{
		Scope:    scopeResponse{Namespace: snapshot.Scope.Namespace, Environment: snapshot.Scope.Environment},
		Revision: snapshot.Revision,
		Flags:    flags,
	})
}

func (h *Handler) get(c *ginframework.Context) {
	scope, err := h.scope(c)
	if err != nil {
		writeError(c, err)
		return
	}
	flag, revision, err := h.manager.Get(c.Request.Context(), scope, c.Param("key"))
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, revision)
	writeJSON(c, http.StatusOK, responseFlag(flag))
}

func (h *Handler) put(c *ginframework.Context) {
	scope, err := h.scope(c)
	if err != nil {
		writeError(c, err)
		return
	}
	expected, err := readExpectedRevision(c.Request)
	if err != nil {
		writeError(c, err)
		return
	}
	var request flagRequest
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, err)
		return
	}
	if request.Key != c.Param("key") {
		writeError(c, toggle.ErrInvalidDefinition)
		return
	}
	revision, err := h.manager.Put(c.Request.Context(), scope, request.flag(), expected)
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, revision)
	writeJSON(c, http.StatusOK, revisionResponse{Revision: revision})
}

func (h *Handler) delete(c *ginframework.Context) {
	scope, err := h.scope(c)
	if err != nil {
		writeError(c, err)
		return
	}
	expected, err := readExpectedRevision(c.Request)
	if err != nil {
		writeError(c, err)
		return
	}
	revision, err := h.manager.Delete(c.Request.Context(), scope, c.Param("key"), expected)
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, revision)
	c.Status(http.StatusNoContent)
}

type flagRequest struct {
	Key         string        `json:"key"`
	Description string        `json:"description"`
	Enabled     bool          `json:"enabled"`
	Rules       []toggle.Rule `json:"rules"`
}

func (r flagRequest) flag() toggle.Flag {
	return toggle.Flag{Key: r.Key, Description: r.Description, Enabled: r.Enabled, Rules: r.Rules}
}

type flagResponse struct {
	Key         string        `json:"key"`
	Description string        `json:"description"`
	Enabled     bool          `json:"enabled"`
	Rules       []toggle.Rule `json:"rules"`
}

func responseFlag(flag toggle.Flag) flagResponse {
	return flagResponse{Key: flag.Key, Description: flag.Description, Enabled: flag.Enabled, Rules: flag.Rules}
}

type scopeResponse struct {
	Namespace   string `json:"namespace"`
	Environment string `json:"environment"`
}

type snapshotResponse struct {
	Scope    scopeResponse  `json:"scope"`
	Revision int64          `json:"revision"`
	Flags    []flagResponse `json:"flags"`
}

type revisionResponse struct {
	Revision int64 `json:"revision"`
}

type errorResponse struct {
	Error string `json:"error"`
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

func readExpectedRevision(request *http.Request) (int64, error) {
	value := request.Header.Get("If-Match")
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

func decodeJSON(c *ginframework.Context, destination any) error {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestTooLarge
		}
		return errInvalidJSON
	}
	if err := jsoncodec.UnmarshalStrictLimit(body, destination, maxRequestBody); err != nil {
		return errInvalidJSON
	}
	return nil
}

func setRevision(c *ginframework.Context, revision int64) {
	c.Header("ETag", `"`+strconv.FormatInt(revision, 10)+`"`)
}

func writeJSON(c *ginframework.Context, status int, value any) {
	body, err := jsoncodec.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"internal_error"}`)
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

func writeError(c *ginframework.Context, err error) {
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
	writeJSON(c, status, errorResponse{Error: code})
}
