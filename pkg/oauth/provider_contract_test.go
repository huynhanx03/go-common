package oauth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/oauth"
)

const (
	testChallenge = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testVerifier  = "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"
)

func TestGoogleProviderContract(t *testing.T) {
	t.Parallel()

	harness := newProviderHarness(t, "google")
	provider, err := oauth.NewGoogleProvider(harness.options())
	if err != nil {
		t.Fatalf("NewGoogleProvider() error = %v", err)
	}
	assertProviderContract(t, provider, harness, oauth.Profile{
		Subject:       "google-subject-1",
		Email:         "alice@example.com",
		EmailVerified: true,
		DisplayName:   "Alice",
		AvatarURL:     "https://images.example.com/alice.png",
	})
}

func TestGitHubProviderContract(t *testing.T) {
	t.Parallel()

	harness := newProviderHarness(t, "github")
	provider, err := oauth.NewGitHubProvider(harness.options())
	if err != nil {
		t.Fatalf("NewGitHubProvider() error = %v", err)
	}
	assertProviderContract(t, provider, harness, oauth.Profile{
		Subject:       "42",
		Email:         "primary@example.com",
		EmailVerified: true,
		DisplayName:   "Alice",
		Login:         "alice",
		AvatarURL:     "https://images.example.com/alice.png",
	})
}

func TestProviderConstructorValidation(t *testing.T) {
	t.Parallel()

	harness := newProviderHarness(t, "google")
	valid := harness.options()
	tests := []struct {
		name   string
		mutate func(*oauth.Options)
	}{
		{name: "client ID", mutate: func(options *oauth.Options) { options.ClientID = "" }},
		{name: "client secret", mutate: func(options *oauth.Options) { options.ClientSecret = "" }},
		{name: "redirect URI", mutate: func(options *oauth.Options) { options.RedirectURI = "" }},
		{name: "HTTP client", mutate: func(options *oauth.Options) { options.HTTPClient = nil }},
		{name: "insecure redirect", mutate: func(options *oauth.Options) { options.RedirectURI = "http://example.com/callback" }},
		{name: "insecure endpoint", mutate: func(options *oauth.Options) { options.Endpoints.TokenURL = "http://example.com/token" }},
		{name: "timeout", mutate: func(options *oauth.Options) { options.Timeout = time.Minute }},
		{name: "body limit", mutate: func(options *oauth.Options) { options.MaxResponseBytes = 8 << 20 }},
		{name: "connection limit", mutate: func(options *oauth.Options) { options.MaxConcurrentRequests = 1000 }},
		{name: "unknown scope", mutate: func(options *oauth.Options) { options.Scopes = []string{"openid", "admin"} }},
		{name: "duplicate scope", mutate: func(options *oauth.Options) { options.Scopes = []string{"openid", "openid"} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := valid
			options.Scopes = append([]string(nil), valid.Scopes...)
			test.mutate(&options)
			if _, err := oauth.NewGoogleProvider(options); !errors.Is(
				err,
				oauth.ErrInvalidConfiguration,
			) {
				t.Fatalf("NewGoogleProvider() error = %v", err)
			}
		})
	}
}

func TestProviderRejectsMalformedBoundedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		want        error
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, contentType: "application/json", body: `{"error":"secret-provider-body"}`, want: oauth.ErrRateLimited},
		{name: "unavailable", status: http.StatusServiceUnavailable, contentType: "application/json", body: `{"error":"secret-provider-body"}`, want: oauth.ErrProviderUnavailable},
		{name: "content type", status: http.StatusOK, contentType: "text/plain", body: `{"sub":"subject"}`, want: oauth.ErrMalformedResponse},
		{name: "unknown field", status: http.StatusOK, contentType: "application/json", body: `{"sub":"subject","unknown":"secret-provider-body"}`, want: oauth.ErrMalformedResponse},
		{name: "duplicate field", status: http.StatusOK, contentType: "application/json", body: `{"sub":"one","sub":"two"}`, want: oauth.ErrMalformedResponse},
		{name: "missing subject", status: http.StatusOK, contentType: "application/json", body: `{"sub":""}`, want: oauth.ErrMissingSubject},
		{name: "oversized", status: http.StatusOK, contentType: "application/json", body: strings.Repeat("x", 2048), want: oauth.ErrResponseTooLarge},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := newProviderHarness(t, "google")
			harness.profileStatus.Store(int64(test.status))
			harness.profileContentType.Store(test.contentType)
			harness.profileBody.Store(test.body)
			options := harness.options()
			options.MaxResponseBytes = 1024
			provider, err := oauth.NewGoogleProvider(options)
			if err != nil {
				t.Fatal(err)
			}
			_, exchangeErr := provider.Exchange(context.Background(), validExchange(harness.redirectURI()))
			if !errors.Is(exchangeErr, test.want) {
				t.Fatalf("Exchange() error = %v, want %v", exchangeErr, test.want)
			}
			if !harness.tokenBodyClosed.Load() || !harness.profileBodyClosed.Load() {
				t.Fatal("error path did not close provider response bodies")
			}
			for _, secret := range []string{
				"authorization-code-secret",
				testVerifier,
				"secret-provider-body",
				"access-token-secret",
				"client-secret",
			} {
				if exchangeErr != nil && strings.Contains(exchangeErr.Error(), secret) {
					t.Fatalf("error leaks %q: %v", secret, exchangeErr)
				}
			}
		})
	}
}

func TestProviderRejectsMalformedBoundedTokenResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		want        error
	}{
		{name: "rejected", status: http.StatusBadRequest, contentType: "application/json", body: `{"error":"secret-provider-body"}`, want: oauth.ErrExchangeRejected},
		{name: "rate limited", status: http.StatusTooManyRequests, contentType: "application/json", body: `{"error":"secret-provider-body"}`, want: oauth.ErrRateLimited},
		{name: "unavailable", status: http.StatusBadGateway, contentType: "application/json", body: `{"error":"secret-provider-body"}`, want: oauth.ErrProviderUnavailable},
		{name: "content type", status: http.StatusOK, contentType: "text/plain", body: `{"access_token":"access-token-secret"}`, want: oauth.ErrMalformedResponse},
		{name: "unknown field", status: http.StatusOK, contentType: "application/json", body: `{"access_token":"access-token-secret","unknown":"secret-provider-body"}`, want: oauth.ErrMalformedResponse},
		{name: "duplicate field", status: http.StatusOK, contentType: "application/json", body: `{"access_token":"one","access_token":"two"}`, want: oauth.ErrMalformedResponse},
		{name: "missing token", status: http.StatusOK, contentType: "application/json", body: `{}`, want: oauth.ErrMalformedResponse},
		{name: "oversized", status: http.StatusOK, contentType: "application/json", body: strings.Repeat("x", 2048), want: oauth.ErrResponseTooLarge},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := newProviderHarness(t, "google")
			harness.tokenStatus.Store(int64(test.status))
			harness.tokenContentType.Store(test.contentType)
			harness.tokenBody.Store(test.body)
			options := harness.options()
			options.MaxResponseBytes = 1024
			provider, err := oauth.NewGoogleProvider(options)
			if err != nil {
				t.Fatal(err)
			}
			_, exchangeErr := provider.Exchange(
				context.Background(),
				validExchange(harness.redirectURI()),
			)
			if !errors.Is(exchangeErr, test.want) {
				t.Fatalf("Exchange() error = %v, want %v", exchangeErr, test.want)
			}
			if !harness.tokenBodyClosed.Load() {
				t.Fatal("token response body was not closed")
			}
			for _, secret := range []string{
				"authorization-code-secret",
				testVerifier,
				"secret-provider-body",
				"access-token-secret",
				"client-secret",
			} {
				if exchangeErr != nil && strings.Contains(exchangeErr.Error(), secret) {
					t.Fatalf("error leaks %q: %v", secret, exchangeErr)
				}
			}
		})
	}
}

func TestProviderRejectsInvalidExchangeAndCancelledAuthorization(t *testing.T) {
	t.Parallel()

	harness := newProviderHarness(t, "google")
	provider, err := oauth.NewGoogleProvider(harness.options())
	if err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.AuthorizationURL(cancelled, oauth.AuthorizationRequest{
		State:         "state",
		PKCEChallenge: testChallenge,
		RedirectURI:   harness.redirectURI(),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("AuthorizationURL(cancelled) error = %v", err)
	}
	if _, err := provider.AuthorizationURL(nil, oauth.AuthorizationRequest{}); !errors.Is(
		err,
		oauth.ErrInvalidRequest,
	) {
		t.Fatalf("AuthorizationURL(nil) error = %v", err)
	}

	valid := validExchange(harness.redirectURI())
	tests := []struct {
		name   string
		mutate func(*oauth.ExchangeRequest)
	}{
		{name: "missing code", mutate: func(request *oauth.ExchangeRequest) { request.Code = "" }},
		{name: "padded code", mutate: func(request *oauth.ExchangeRequest) { request.Code = " code" }},
		{name: "oversized code", mutate: func(request *oauth.ExchangeRequest) { request.Code = strings.Repeat("x", 2049) }},
		{name: "short verifier", mutate: func(request *oauth.ExchangeRequest) { request.PKCEVerifier = "short" }},
		{name: "invalid verifier", mutate: func(request *oauth.ExchangeRequest) { request.PKCEVerifier = strings.Repeat("/", 43) }},
		{name: "redirect mismatch", mutate: func(request *oauth.ExchangeRequest) { request.RedirectURI += "/wrong" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := valid
			test.mutate(&request)
			if _, err := provider.Exchange(context.Background(), request); !errors.Is(
				err,
				oauth.ErrInvalidRequest,
			) {
				t.Fatalf("Exchange() error = %v", err)
			}
		})
	}
	if _, err := provider.Exchange(nil, valid); !errors.Is(err, oauth.ErrInvalidRequest) {
		t.Fatalf("Exchange(nil) error = %v", err)
	}
	if _, err := provider.Exchange(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Exchange(cancelled) error = %v", err)
	}
}

func TestExplicitCompatibilityProvider(t *testing.T) {
	t.Parallel()

	disabledHarness := newProviderHarness(t, "google")
	disabled, err := oauth.NewGoogleProvider(disabledHarness.options())
	if err != nil {
		t.Fatal(err)
	}
	if authURL := disabled.AuthCodeURL("state"); authURL != "" {
		t.Fatalf("disabled AuthCodeURL() = %q", authURL)
	}
	if _, err := disabled.ExchangeCode(
		context.Background(),
		"authorization-code-secret",
	); !errors.Is(err, oauth.ErrCompatibilityDisabled) {
		t.Fatalf("disabled ExchangeCode() error = %v", err)
	}

	enabledHarness := newProviderHarness(t, "google")
	options := enabledHarness.options()
	options.EnableCompatibility = true
	enabled, err := oauth.NewGoogleProvider(options)
	if err != nil {
		t.Fatal(err)
	}
	authURL := enabled.AuthCodeURL("state")
	if authURL == "" || strings.Contains(authURL, "code_challenge") {
		t.Fatalf("compatibility AuthCodeURL() = %q", authURL)
	}
	enabledHarness.expectPKCE.Store(false)
	info, err := enabled.ExchangeCode(
		context.Background(),
		"authorization-code-secret",
	)
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}
	if info.ExternalID != "google-subject-1" ||
		info.Metadata["email"] != "alice@example.com" ||
		info.Metadata["verified"] != true {
		t.Fatalf("ExchangeCode() = %#v", info)
	}
}

func TestVerifiedEmailSemantics(t *testing.T) {
	t.Parallel()

	t.Run("google unverified", func(t *testing.T) {
		t.Parallel()

		harness := newProviderHarness(t, "google")
		harness.profileBody.Store(`{"sub":"subject","email":"unverified@example.com","email_verified":false,"name":"Alice","picture":""}`)
		provider, err := oauth.NewGoogleProvider(harness.options())
		if err != nil {
			t.Fatal(err)
		}
		profile, err := provider.Exchange(
			context.Background(),
			validExchange(harness.redirectURI()),
		)
		if err != nil {
			t.Fatal(err)
		}
		if profile.Email != "unverified@example.com" || profile.EmailVerified {
			t.Fatalf("profile = %#v", profile)
		}
	})

	t.Run("github public email is not implicitly verified", func(t *testing.T) {
		t.Parallel()

		harness := newProviderHarness(t, "github")
		harness.emailsBody.Store(`[]`)
		provider, err := oauth.NewGitHubProvider(harness.options())
		if err != nil {
			t.Fatal(err)
		}
		profile, err := provider.Exchange(
			context.Background(),
			validExchange(harness.redirectURI()),
		)
		if err != nil {
			t.Fatal(err)
		}
		if profile.Email != "public@example.com" || profile.EmailVerified {
			t.Fatalf("profile = %#v", profile)
		}
	})
}

func TestProviderCancellationTimeoutAndConnectionLimit(t *testing.T) {
	t.Parallel()

	harness := newProviderHarness(t, "google")
	harness.blockProfile.Store(true)
	options := harness.options()
	options.Timeout = 60 * time.Millisecond
	options.MaxConcurrentRequests = 1
	provider, err := oauth.NewGoogleProvider(options)
	if err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Exchange(cancelled, validExchange(harness.redirectURI())); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Exchange(cancelled) error = %v", err)
	}

	started := time.Now()
	if _, err := provider.Exchange(context.Background(), validExchange(harness.redirectURI())); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("Exchange(timeout) error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("provider total timeout was not enforced")
	}
	deadline := time.Now().Add(time.Second)
	for harness.inFlight.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if inFlight := harness.inFlight.Load(); inFlight != 0 {
		t.Fatalf("cancelled provider request still in flight = %d", inFlight)
	}

	harness.blockProfile.Store(false)
	harness.maximumConcurrent.Store(0)
	harness.profileDelay.Store(int64(40 * time.Millisecond))
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			_, _ = provider.Exchange(context.Background(), validExchange(harness.redirectURI()))
		}()
	}
	wait.Wait()
	if maximum := harness.maximumConcurrent.Load(); maximum > 1 {
		t.Fatalf("maximum concurrent provider requests = %d", maximum)
	}
}

func assertProviderContract(
	t *testing.T,
	provider oauth.Provider,
	harness *providerHarness,
	want oauth.Profile,
) {
	t.Helper()

	redirectURI := harness.redirectURI()
	authURL, err := provider.AuthorizationURL(context.Background(), oauth.AuthorizationRequest{
		State:         "state-1",
		PKCEChallenge: testChallenge,
		RedirectURI:   redirectURI,
	})
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for key, value := range map[string]string{
		"client_id":             "client-id",
		"state":                 "state-1",
		"redirect_uri":          redirectURI,
		"response_type":         "code",
		"code_challenge":        testChallenge,
		"code_challenge_method": "S256",
	} {
		if query.Get(key) != value {
			t.Errorf("%s = %q, want %q", key, query.Get(key), value)
		}
	}

	profile, err := provider.Exchange(context.Background(), validExchange(redirectURI))
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if profile != want {
		t.Fatalf("Exchange() = %#v, want %#v", profile, want)
	}
	if !harness.tokenBodyClosed.Load() || !harness.profileBodyClosed.Load() {
		t.Fatal("provider response body was not closed")
	}

	invalidRequests := []oauth.AuthorizationRequest{
		{PKCEChallenge: testChallenge, RedirectURI: redirectURI},
		{State: "state", RedirectURI: redirectURI},
		{State: "state", PKCEChallenge: "not-s256", RedirectURI: redirectURI},
		{State: "state", PKCEChallenge: testChallenge, RedirectURI: redirectURI + "/wrong"},
	}
	for _, request := range invalidRequests {
		if _, err := provider.AuthorizationURL(context.Background(), request); !errors.Is(
			err,
			oauth.ErrInvalidRequest,
		) {
			t.Errorf("AuthorizationURL(%#v) error = %v", request, err)
		}
	}
}

func validExchange(redirectURI string) oauth.ExchangeRequest {
	return oauth.ExchangeRequest{
		Code:         "authorization-code-secret",
		PKCEVerifier: testVerifier,
		RedirectURI:  redirectURI,
	}
}

type providerHarness struct {
	t                  *testing.T
	kind               string
	server             *httptest.Server
	profileStatus      atomic.Int64
	profileContentType atomic.Value
	profileBody        atomic.Value
	profileDelay       atomic.Int64
	blockProfile       atomic.Bool
	inFlight           atomic.Int64
	maximumConcurrent  atomic.Int64
	tokenBodyClosed    atomic.Bool
	profileBodyClosed  atomic.Bool
	tokenStatus        atomic.Int64
	tokenContentType   atomic.Value
	tokenBody          atomic.Value
	emailsBody         atomic.Value
	expectPKCE         atomic.Bool
}

func newProviderHarness(t *testing.T, kind string) *providerHarness {
	t.Helper()

	harness := &providerHarness{t: t, kind: kind}
	harness.profileStatus.Store(http.StatusOK)
	harness.profileContentType.Store("application/json")
	harness.tokenStatus.Store(http.StatusOK)
	harness.tokenContentType.Store("application/json")
	harness.tokenBody.Store(`{"access_token":"access-token-secret","token_type":"Bearer","expires_in":3600}`)
	harness.emailsBody.Store(`[{"email":"other@example.com","primary":false,"verified":true},{"email":"primary@example.com","primary":true,"verified":true}]`)
	harness.expectPKCE.Store(true)
	if kind == "google" {
		harness.profileBody.Store(`{"sub":"google-subject-1","email":"alice@example.com","email_verified":true,"name":"Alice","picture":"https://images.example.com/alice.png"}`)
	} else {
		harness.profileBody.Store(`{"id":42,"login":"alice","name":"Alice","email":"public@example.com","avatar_url":"https://images.example.com/alice.png"}`)
	}
	harness.server = httptest.NewTLSServer(http.HandlerFunc(harness.serveHTTP))
	t.Cleanup(harness.server.Close)
	return harness
}

func (h *providerHarness) options() oauth.Options {
	client := h.server.Client()
	client.Transport = &trackingTransport{
		base:          client.Transport,
		tokenClosed:   &h.tokenBodyClosed,
		profileClosed: &h.profileBodyClosed,
	}
	return oauth.Options{
		ClientID:              "client-id",
		ClientSecret:          "client-secret",
		RedirectURI:           h.redirectURI(),
		HTTPClient:            client,
		Timeout:               2 * time.Second,
		MaxResponseBytes:      1024,
		MaxConcurrentRequests: 2,
		Endpoints: oauth.Endpoints{
			AuthorizationURL: h.server.URL + "/authorize",
			TokenURL:         h.server.URL + "/token",
			ProfileURL:       h.server.URL + "/profile",
			EmailsURL:        h.server.URL + "/emails",
		},
	}
}

func (h *providerHarness) redirectURI() string {
	return h.server.URL + "/callback"
}

func (h *providerHarness) serveHTTP(response http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/token":
		h.handleToken(response, request)
	case "/profile":
		h.handleProfile(response, request)
	case "/emails":
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, h.emailsBody.Load().(string))
	default:
		http.NotFound(response, request)
	}
}

func (h *providerHarness) handleToken(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		h.t.Errorf("parse token form: %v", err)
	}
	for key, value := range map[string]string{
		"code":         "authorization-code-secret",
		"redirect_uri": h.redirectURI(),
	} {
		if request.Form.Get(key) != value {
			h.t.Errorf("token %s = %q, want %q", key, request.Form.Get(key), value)
		}
	}
	if verifier := request.Form.Get("code_verifier"); h.expectPKCE.Load() {
		if verifier != testVerifier {
			h.t.Errorf("token code_verifier = %q, want %q", verifier, testVerifier)
		}
	} else if verifier != "" {
		h.t.Errorf("legacy token code_verifier = %q, want empty", verifier)
	}
	response.Header().Set("Content-Type", h.tokenContentType.Load().(string))
	response.WriteHeader(int(h.tokenStatus.Load()))
	_, _ = io.WriteString(response, h.tokenBody.Load().(string))
}

func (h *providerHarness) handleProfile(response http.ResponseWriter, request *http.Request) {
	current := h.inFlight.Add(1)
	defer h.inFlight.Add(-1)
	for {
		maximum := h.maximumConcurrent.Load()
		if current <= maximum || h.maximumConcurrent.CompareAndSwap(maximum, current) {
			break
		}
	}
	if request.Header.Get("Authorization") != "Bearer access-token-secret" {
		h.t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
	}
	if h.blockProfile.Load() {
		<-request.Context().Done()
		return
	}
	if delay := time.Duration(h.profileDelay.Load()); delay > 0 {
		time.Sleep(delay)
	}
	response.Header().Set("Content-Type", h.profileContentType.Load().(string))
	response.WriteHeader(int(h.profileStatus.Load()))
	_, _ = io.WriteString(response, h.profileBody.Load().(string))
}

func (h *providerHarness) String() string {
	return fmt.Sprintf("providerHarness(%s)", h.kind)
}

type trackingTransport struct {
	base          http.RoundTripper
	tokenClosed   *atomic.Bool
	profileClosed *atomic.Bool
}

func (t *trackingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	closed := t.profileClosed
	if request.URL.Path == "/token" {
		closed = t.tokenClosed
	}
	response.Body = &trackingBody{
		ReadCloser: response.Body,
		closed:     closed,
	}
	return response, nil
}

type trackingBody struct {
	io.ReadCloser
	closed *atomic.Bool
}

func (b *trackingBody) Close() error {
	err := b.ReadCloser.Close()
	b.closed.Store(true)
	return err
}
