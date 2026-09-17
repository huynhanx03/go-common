package oauth

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/oauth2"
)

const (
	defaultTimeout               = 10 * time.Second
	maxTimeout                   = 30 * time.Second
	defaultMaxResponseBytes      = int64(1 << 20)
	hardMaxResponseBytes         = int64(4 << 20)
	defaultMaxConcurrentRequests = 8
	hardMaxConcurrentRequests    = 64
	maxClientIDBytes             = 512
	maxClientSecretBytes         = 4096
	maxRedirectURIBytes          = 2048
	maxEndpointBytes             = 2048
	maxScopeBytes                = 128
)

// Endpoints lets tests and self-hosted providers supply explicit OAuth URLs.
// All URLs must be HTTPS unless loopback-only development mode is enabled.
type Endpoints struct {
	AuthorizationURL string
	TokenURL         string
	ProfileURL       string
	EmailsURL        string
}

// Options is the shared bounded configuration for Google and GitHub adapters.
type Options struct {
	ClientID              string
	ClientSecret          string
	RedirectURI           string
	Scopes                []string
	Endpoints             Endpoints
	HTTPClient            *http.Client
	Timeout               time.Duration
	MaxResponseBytes      int64
	MaxConcurrentRequests int

	AllowInsecureDevelopment bool
	EnableCompatibility      bool
}

type providerDefaults struct {
	endpoints      Endpoints
	scopes         []string
	allowedScopes  map[string]struct{}
	requiredScopes []string
	authStyle      oauth2.AuthStyle
	requireEmails  bool
}

type normalizedOptions struct {
	clientID              string
	clientSecret          string
	redirectURI           string
	scopes                []string
	endpoints             Endpoints
	httpClient            *http.Client
	timeout               time.Duration
	maxResponseBytes      int64
	maxConcurrentRequests int
	enableCompatibility   bool
	authStyle             oauth2.AuthStyle
}

func normalizeOptions(
	provider string,
	options Options,
	defaults providerDefaults,
) (normalizedOptions, error) {
	if err := validateBoundedValue("client ID", options.ClientID, maxClientIDBytes); err != nil {
		return normalizedOptions{}, configurationError(provider)
	}
	if err := validateBoundedValue("client secret", options.ClientSecret, maxClientSecretBytes); err != nil {
		return normalizedOptions{}, configurationError(provider)
	}
	if options.HTTPClient == nil {
		return normalizedOptions{}, configurationError(provider)
	}
	if err := validateAbsoluteURL(
		options.RedirectURI,
		options.AllowInsecureDevelopment,
		true,
	); err != nil {
		return normalizedOptions{}, configurationError(provider)
	}

	endpoints := mergeEndpoints(options.Endpoints, defaults.endpoints)
	for _, endpoint := range []string{
		endpoints.AuthorizationURL,
		endpoints.TokenURL,
		endpoints.ProfileURL,
	} {
		if err := validateAbsoluteURL(
			endpoint,
			options.AllowInsecureDevelopment,
			false,
		); err != nil {
			return normalizedOptions{}, configurationError(provider)
		}
	}
	if defaults.requireEmails {
		if err := validateAbsoluteURL(
			endpoints.EmailsURL,
			options.AllowInsecureDevelopment,
			false,
		); err != nil {
			return normalizedOptions{}, configurationError(provider)
		}
	} else if endpoints.EmailsURL != "" {
		if err := validateAbsoluteURL(
			endpoints.EmailsURL,
			options.AllowInsecureDevelopment,
			false,
		); err != nil {
			return normalizedOptions{}, configurationError(provider)
		}
	}

	scopes := options.Scopes
	if len(scopes) == 0 {
		scopes = defaults.scopes
	}
	scopes = slices.Clone(scopes)
	if err := validateScopes(scopes, defaults.allowedScopes, defaults.requiredScopes); err != nil {
		return normalizedOptions{}, configurationError(provider)
	}

	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout <= 0 || timeout > maxTimeout {
		return normalizedOptions{}, configurationError(provider)
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	if maxResponseBytes < 256 || maxResponseBytes > hardMaxResponseBytes {
		return normalizedOptions{}, configurationError(provider)
	}
	maxConcurrentRequests := options.MaxConcurrentRequests
	if maxConcurrentRequests == 0 {
		maxConcurrentRequests = defaultMaxConcurrentRequests
	}
	if maxConcurrentRequests < 1 || maxConcurrentRequests > hardMaxConcurrentRequests {
		return normalizedOptions{}, configurationError(provider)
	}

	client := boundedHTTPClient(options.HTTPClient, timeout, maxConcurrentRequests)
	return normalizedOptions{
		clientID:              options.ClientID,
		clientSecret:          options.ClientSecret,
		redirectURI:           options.RedirectURI,
		scopes:                scopes,
		endpoints:             endpoints,
		httpClient:            client,
		timeout:               timeout,
		maxResponseBytes:      maxResponseBytes,
		maxConcurrentRequests: maxConcurrentRequests,
		enableCompatibility:   options.EnableCompatibility,
		authStyle:             defaults.authStyle,
	}, nil
}

func mergeEndpoints(configured, defaults Endpoints) Endpoints {
	if configured.AuthorizationURL == "" {
		configured.AuthorizationURL = defaults.AuthorizationURL
	}
	if configured.TokenURL == "" {
		configured.TokenURL = defaults.TokenURL
	}
	if configured.ProfileURL == "" {
		configured.ProfileURL = defaults.ProfileURL
	}
	if configured.EmailsURL == "" {
		configured.EmailsURL = defaults.EmailsURL
	}
	return configured
}

func validateScopes(
	scopes []string,
	allowed map[string]struct{},
	required []string,
) error {
	if len(scopes) == 0 || len(scopes) > len(allowed) {
		return ErrInvalidConfiguration
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if err := validateBoundedValue("scope", scope, maxScopeBytes); err != nil {
			return err
		}
		if _, ok := allowed[scope]; !ok {
			return ErrInvalidConfiguration
		}
		if _, duplicate := seen[scope]; duplicate {
			return ErrInvalidConfiguration
		}
		seen[scope] = struct{}{}
	}
	for _, requiredScope := range required {
		if _, ok := seen[requiredScope]; !ok {
			return ErrInvalidConfiguration
		}
	}
	return nil
}

func validateAbsoluteURL(raw string, allowDevelopment, callback bool) error {
	maxBytes := maxEndpointBytes
	if callback {
		maxBytes = maxRedirectURIBytes
	}
	if err := validateBoundedValue("URL", raw, maxBytes); err != nil {
		return err
	}
	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.Scheme == "" ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return ErrInvalidConfiguration
	}
	if !callback && (parsed.RawQuery != "" || parsed.ForceQuery) {
		return ErrInvalidConfiguration
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" || !allowDevelopment || !isLoopbackHost(parsed.Hostname()) {
		return ErrInvalidConfiguration
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func validateBoundedValue(name, value string, maxBytes int) error {
	if value == "" || len(value) > maxBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: invalid %s", ErrInvalidConfiguration, name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: invalid %s", ErrInvalidConfiguration, name)
		}
	}
	return nil
}

func configurationError(provider string) error {
	return newProviderError(ErrInvalidConfiguration, provider, "configure")
}

type boundedRoundTripper struct {
	base  http.RoundTripper
	slots chan struct{}
}

func (t *boundedRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	select {
	case t.slots <- struct{}{}:
	case <-request.Context().Done():
		return nil, request.Context().Err()
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		<-t.slots
		return nil, err
	}
	response.Body = &releaseBody{
		ReadCloser: response.Body,
		release:    func() { <-t.slots },
	}
	return response, nil
}

func boundedHTTPClient(input *http.Client, timeout time.Duration, limit int) *http.Client {
	client := *input
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if transport, ok := base.(*http.Transport); ok {
		transport = transport.Clone()
		transport.MaxConnsPerHost = limit
		if transport.MaxIdleConnsPerHost == 0 || transport.MaxIdleConnsPerHost > limit {
			transport.MaxIdleConnsPerHost = limit
		}
		base = transport
	}
	client.Transport = &boundedRoundTripper{
		base:  base,
		slots: make(chan struct{}, limit),
	}
	if client.Timeout == 0 || client.Timeout > timeout {
		client.Timeout = timeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client.Jar = nil
	return &client
}

type releaseBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
	err     error
}

func (b *releaseBody) Close() error {
	b.once.Do(func() {
		b.err = b.ReadCloser.Close()
		b.release()
	})
	return b.err
}
