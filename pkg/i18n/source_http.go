package i18n

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
)

// HTTPSource reads published catalogs from the i18n management API. The
// provided client can attach service credentials with its transport.
type HTTPSource struct {
	base   *url.URL
	client *http.Client
}

func NewHTTPSource(baseURL string, client *http.Client) (*HTTPSource, error) {
	base, err := url.Parse(baseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, ErrInvalidConfig
	}
	if client == nil {
		client = http.DefaultClient
	}
	base.Path = strings.TrimRight(base.Path, "/")
	return &HTTPSource{base: base, client: client}, nil
}

func (s *HTTPSource) Info(ctx context.Context, domain string) (CatalogInfo, error) {
	var info CatalogInfo
	if err := s.get(ctx, domain, "catalog/manifest", "", &info, maxInfoBytes); err != nil {
		return CatalogInfo{}, err
	}
	if err := validateInfo(domain, info); err != nil {
		return CatalogInfo{}, ErrInvalidDefinition
	}
	return info, nil
}

func (s *HTTPSource) LoadLocale(ctx context.Context, domain, locale, version string) (Catalog, error) {
	if _, err := parseLocale(locale); err != nil || !validVersion(version) {
		return Catalog{}, ErrInvalidConfig
	}
	path := "catalog/locales/" + url.PathEscape(locale)
	var catalog Catalog
	if err := s.get(ctx, domain, path, version, &catalog, MaxCatalogBytes); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (s *HTTPSource) get(ctx context.Context, domain, suffix, version string, output any, limit int64) error {
	if s == nil || s.base == nil || s.client == nil || ValidateDomain(domain) != nil {
		return ErrInvalidConfig
	}
	if err := contextOK(ctx); err != nil {
		return err
	}
	requestURL := *s.base
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + "/v1/i18n/domains/" + url.PathEscape(domain) + "/" + suffix
	if version != "" {
		query := requestURL.Query()
		query.Set("version", version)
		requestURL.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return ErrInvalidConfig
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return sourceError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusPreconditionFailed {
			return ErrStaleRevision
		}
		if response.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		return sourceError(ErrSource)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return sourceError(err)
	}
	if int64(len(body)) > limit {
		return ErrLimit
	}
	if err := jsoncodec.UnmarshalStrictLimit(body, output, int(limit)); err != nil {
		return ErrInvalidDefinition
	}
	return nil
}

var _ Source = (*HTTPSource)(nil)
