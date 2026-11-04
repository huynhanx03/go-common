// Package gin exposes authenticated catalog management and read-only catalog routes.
package gin

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	ginframework "github.com/gin-gonic/gin"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/i18n"
)

type Handler struct {
	manager    *i18n.Manager
	authorizer Authorizer
}

func New(manager *i18n.Manager, authorizer Authorizer) (*Handler, error) {
	if manager == nil || nilValue(authorizer) {
		return nil, i18n.ErrInvalidConfig
	}
	return &Handler{manager: manager, authorizer: authorizer}, nil
}

func (h *Handler) RegisterRoutes(routes ginframework.IRoutes) {
	routes.Handle(http.MethodGet, basePath, h.noStore(h.domains))
	routes.Handle(http.MethodPut, basePath+"/:domain", h.noStore(h.configure))
	routes.Handle(http.MethodGet, basePath+"/:domain", h.noStore(h.get))
	routes.Handle(http.MethodDelete, basePath+"/:domain", h.noStore(h.deleteDomain))
	routes.Handle(http.MethodGet, basePath+"/:domain/keys", h.noStore(h.listKeys))
	routes.Handle(http.MethodPut, basePath+"/:domain/keys/:key", h.noStore(h.putKey))
	routes.Handle(http.MethodDelete, basePath+"/:domain/keys/:key", h.noStore(h.deleteKey))
	routes.Handle(http.MethodPost, basePath+"/:domain/sync", h.noStore(h.syncKeys))
	routes.Handle(http.MethodPut, basePath+"/:domain/keys/:key/translations/:locale", h.noStore(h.putTranslation))
	routes.Handle(http.MethodDelete, basePath+"/:domain/keys/:key/translations/:locale", h.noStore(h.deleteTranslation))
	routes.Handle(http.MethodPost, basePath+"/:domain/keys/:key/translations/:locale/review", h.noStore(h.reviewTranslation))
	routes.Handle(http.MethodGet, basePath+"/:domain/coverage", h.noStore(h.coverage))
	routes.Handle(http.MethodPost, basePath+"/:domain/publish", h.noStore(h.publish))
	routes.Handle(http.MethodGet, basePath+"/:domain/catalog", h.noStore(h.catalog))
	routes.Handle(http.MethodGet, basePath+"/:domain/catalog/manifest", h.noStore(h.catalogInfo))
	routes.Handle(http.MethodGet, basePath+"/:domain/catalog/locales/:locale", h.noStore(h.catalogLocale))
}

func (h *Handler) noStore(next ginframework.HandlerFunc) ginframework.HandlerFunc {
	return func(c *ginframework.Context) {
		c.Header("Cache-Control", "no-store")
		next(c)
	}
}

func (h *Handler) authorize(c *ginframework.Context, action string) (string, error) {
	domain := c.Param("domain")
	if i18n.ValidateDomain(domain) != nil {
		return "", i18n.ErrInvalidDefinition
	}
	if err := h.authorizer.Authorize(c.Request.Context(), domain, action); err != nil {
		return "", err
	}
	return domain, nil
}

func (h *Handler) domains(c *ginframework.Context) {
	domains, err := h.manager.Domains(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	visible := make([]string, 0, len(domains))
	for _, domain := range domains {
		err := h.authorizer.Authorize(c.Request.Context(), domain, ActionDomainRead)
		switch {
		case err == nil:
			visible = append(visible, domain)
		case errors.Is(err, ErrForbidden):
			continue
		default:
			writeError(c, err)
			return
		}
	}
	writeJSON(c, http.StatusOK, visible)
}

func (h *Handler) configure(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionDomainWrite)
	if err != nil {
		writeError(c, err)
		return
	}
	expected, err := readExpectedRevision(c.Request)
	if err != nil {
		writeError(c, err)
		return
	}
	var request i18n.DomainConfig
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, err)
		return
	}
	revision, err := h.manager.Configure(c.Request.Context(), domain, request, expected)
	if err != nil {
		writeError(c, err)
		return
	}
	writeRevision(c, revision)
}

func (h *Handler) get(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionDomainRead)
	if err != nil {
		writeError(c, err)
		return
	}
	state, err := h.manager.Get(c.Request.Context(), domain)
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, state.Draft.Revision)
	writeJSON(c, http.StatusOK, state)
}

func (h *Handler) deleteDomain(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionDomainDelete)
	if err != nil {
		writeError(c, err)
		return
	}
	expected, err := readExpectedRevision(c.Request)
	if err == nil {
		err = h.manager.Delete(c.Request.Context(), domain, expected)
	}
	if err != nil {
		writeCommittedRevision(c, err)
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) listKeys(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionDomainRead)
	if err != nil {
		writeError(c, err)
		return
	}
	limit := 0
	if value := c.Query("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			writeError(c, i18n.ErrInvalidDefinition)
			return
		}
	}
	page, err := h.manager.ListKeys(c.Request.Context(), domain, i18n.Query{
		Search: c.Query("search"), MissingLocale: c.Query("missing_locale"), After: c.Query("after"), Limit: limit,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, page.Revision)
	writeJSON(c, http.StatusOK, page)
}

func (h *Handler) putKey(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionKeyWrite)
	if err != nil {
		writeError(c, err)
		return
	}
	expected, err := readExpectedRevision(c.Request)
	if err != nil {
		writeError(c, err)
		return
	}
	var definition i18n.Definition
	if err := decodeJSON(c, &definition); err != nil {
		writeError(c, err)
		return
	}
	if definition.Key != c.Param("key") {
		writeError(c, i18n.ErrInvalidDefinition)
		return
	}
	revision, err := h.manager.PutKey(c.Request.Context(), domain, definition, expected)
	if err != nil {
		writeError(c, err)
		return
	}
	writeRevision(c, revision)
}

func (h *Handler) deleteKey(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionKeyWrite)
	if err == nil {
		var revision int64
		revision, err = readExpectedRevision(c.Request)
		if err == nil {
			revision, err = h.manager.DeleteKey(c.Request.Context(), domain, c.Param("key"), revision)
		}
		if err == nil {
			writeRevision(c, revision)
			return
		}
	}
	writeError(c, err)
}

func (h *Handler) syncKeys(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionKeyWrite)
	if err != nil {
		writeError(c, err)
		return
	}
	expected, err := readExpectedRevision(c.Request)
	if err != nil {
		writeError(c, err)
		return
	}
	var request struct {
		Keys []i18n.Definition `json:"keys"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, err)
		return
	}
	revision, err := h.manager.Sync(c.Request.Context(), domain, request.Keys, expected)
	if err != nil {
		writeError(c, err)
		return
	}
	writeRevision(c, revision)
}

func (h *Handler) putTranslation(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionTranslationWrite)
	if err != nil {
		writeError(c, err)
		return
	}
	expected, err := readExpectedRevision(c.Request)
	if err != nil {
		writeError(c, err)
		return
	}
	var forms i18n.Forms
	if err := decodeJSON(c, &forms); err != nil {
		writeError(c, err)
		return
	}
	revision, err := h.manager.PutTranslation(c.Request.Context(), domain, c.Param("key"), c.Param("locale"), forms, expected)
	if err != nil {
		writeError(c, err)
		return
	}
	writeRevision(c, revision)
}

func (h *Handler) deleteTranslation(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionTranslationWrite)
	if err == nil {
		revision, revisionErr := readExpectedRevision(c.Request)
		err = revisionErr
		if err == nil {
			revision, err = h.manager.DeleteTranslation(c.Request.Context(), domain, c.Param("key"), c.Param("locale"), revision)
		}
		if err == nil {
			writeRevision(c, revision)
			return
		}
	}
	writeError(c, err)
}

func (h *Handler) reviewTranslation(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionTranslationReview)
	if err == nil {
		var revision int64
		revision, err = readExpectedRevision(c.Request)
		if err == nil {
			revision, err = h.manager.Review(c.Request.Context(), domain, c.Param("key"), c.Param("locale"), revision)
		}
		if err == nil {
			writeRevision(c, revision)
			return
		}
	}
	writeError(c, err)
}

func (h *Handler) coverage(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionDomainRead)
	if err != nil {
		writeError(c, err)
		return
	}
	coverage, err := h.manager.Coverage(c.Request.Context(), domain)
	if err != nil {
		writeError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, coverage)
}

func (h *Handler) publish(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionCatalogPublish)
	if err == nil {
		var revision int64
		revision, err = readExpectedRevision(c.Request)
		if err == nil {
			revision, err = h.manager.Publish(c.Request.Context(), domain, revision)
		}
		if err == nil {
			writeRevision(c, revision)
			return
		}
	}
	writeCommittedRevision(c, err)
	writeError(c, err)
}

func writeCommittedRevision(c *ginframework.Context, err error) {
	var committed *i18n.CommittedError
	if errors.As(err, &committed) {
		setRevision(c, committed.Revision)
	}
}

func (h *Handler) catalogInfo(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionCatalogRead)
	if err != nil {
		writeError(c, err)
		return
	}
	info, err := h.manager.Info(c.Request.Context(), domain)
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, info.Revision)
	writeJSON(c, http.StatusOK, info)
}

func (h *Handler) catalogLocale(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionCatalogRead)
	if err != nil {
		writeError(c, err)
		return
	}
	catalog, err := h.manager.LoadLocale(c.Request.Context(), domain, c.Param("locale"), c.Query("version"))
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, catalog.Revision)
	writeJSON(c, http.StatusOK, catalog)
}

func (h *Handler) catalog(c *ginframework.Context) {
	domain, err := h.authorize(c, ActionCatalogRead)
	if err != nil {
		writeError(c, err)
		return
	}
	catalog, err := h.manager.Load(c.Request.Context(), domain)
	if err != nil {
		writeError(c, err)
		return
	}
	setRevision(c, catalog.Revision)
	writeJSON(c, http.StatusOK, catalog)
}

type errorResponse struct {
	Error string `json:"error"`
}

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

func decodeJSON(c *ginframework.Context, target any) error {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, i18n.MaxCatalogBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestTooLarge
		}
		return errInvalidJSON
	}
	if err := jsoncodec.UnmarshalStrictLimit(body, target, i18n.MaxCatalogBytes); err != nil {
		return errInvalidJSON
	}
	return nil
}

func writeRevision(c *ginframework.Context, revision int64) {
	setRevision(c, revision)
	writeJSON(c, http.StatusOK, struct {
		Revision int64 `json:"revision"`
	}{revision})
}

func setRevision(c *ginframework.Context, revision int64) {
	c.Header("ETag", `"`+strconv.FormatInt(revision, 10)+`"`)
}

func writeJSON(c *ginframework.Context, status int, value any) {
	body, err := jsoncodec.Marshal(value)
	if err != nil {
		status, body = http.StatusInternalServerError, []byte(`{"error":"internal_error"}`)
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

func writeError(c *ginframework.Context, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	var requestErr requestError
	switch {
	case errors.As(err, &requestErr):
		status, code = requestErr.status, requestErr.code
	case errors.Is(err, i18n.ErrInvalidation):
		status, code = http.StatusServiceUnavailable, "catalog_change_committed_invalidation_pending"
	case errors.Is(err, i18n.ErrSource):
		status, code = http.StatusInternalServerError, "internal_error"
	case errors.Is(err, ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, i18n.ErrInvalidConfig), errors.Is(err, i18n.ErrInvalidDefinition), errors.Is(err, i18n.ErrInvalidParams):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, i18n.ErrLimit):
		status, code = http.StatusRequestEntityTooLarge, "limit_exceeded"
	case errors.Is(err, i18n.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, i18n.ErrConflict), errors.Is(err, i18n.ErrStaleRevision):
		status, code = http.StatusPreconditionFailed, "revision_conflict"
	case errors.Is(err, i18n.ErrIncomplete):
		status, code = http.StatusConflict, "catalog_incomplete"
	}
	writeJSON(c, status, errorResponse{Error: code})
}
