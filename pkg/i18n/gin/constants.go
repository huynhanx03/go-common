package gin

import "net/http"

const basePath = "/v1/i18n/domains"

const (
	ActionDomainRead        = "domain:read"
	ActionDomainWrite       = "domain:write"
	ActionDomainDelete      = "domain:delete"
	ActionKeyWrite          = "key:write"
	ActionTranslationWrite  = "translation:write"
	ActionTranslationReview = "translation:review"
	ActionCatalogPublish    = "catalog:publish"
	ActionCatalogRead       = "catalog:read"
)

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
