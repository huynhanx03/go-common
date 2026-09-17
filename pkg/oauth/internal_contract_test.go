package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

func TestNormalizeProfileBoundaries(t *testing.T) {
	t.Parallel()

	valid := Profile{
		Subject:       "subject-1",
		Email:         "alice@example.com",
		EmailVerified: true,
		DisplayName:   "Alice Example",
		Login:         "alice",
		AvatarURL:     "https://images.example.com/alice.png",
	}
	if got, err := normalizeProfile("test", valid); err != nil || got != valid {
		t.Fatalf("normalizeProfile(valid) = %#v, %v", got, err)
	}

	tests := []struct {
		name string
		want error
		edit func(*Profile)
	}{
		{name: "control", want: ErrMalformedResponse, edit: func(profile *Profile) { profile.DisplayName = "Alice\n" }},
		{name: "missing subject", want: ErrMissingSubject, edit: func(profile *Profile) { profile.Subject = " " }},
		{name: "oversized subject", want: ErrMalformedResponse, edit: func(profile *Profile) { profile.Subject = strings.Repeat("x", maxSubjectBytes+1) }},
		{name: "subject space", want: ErrMalformedResponse, edit: func(profile *Profile) { profile.Subject = "subject one" }},
		{name: "malformed email", want: ErrMalformedResponse, edit: func(profile *Profile) { profile.Email = "not-an-email" }},
		{name: "display too long", want: ErrMalformedResponse, edit: func(profile *Profile) { profile.DisplayName = strings.Repeat("x", maxDisplayBytes+1) }},
		{name: "login space", want: ErrMalformedResponse, edit: func(profile *Profile) { profile.Login = "alice user" }},
		{name: "avatar HTTP", want: ErrMalformedResponse, edit: func(profile *Profile) { profile.AvatarURL = "http://images.example.com/a.png" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			profile := valid
			test.edit(&profile)
			if _, err := normalizeProfile("test", profile); !errors.Is(err, test.want) {
				t.Fatalf("normalizeProfile() error = %v, want %v", err, test.want)
			}
		})
	}

	withoutEmail := valid
	withoutEmail.Email = ""
	withoutEmail.EmailVerified = true
	got, err := normalizeProfile("test", withoutEmail)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmailVerified {
		t.Fatal("empty email remained verified")
	}
}

func TestOptionDefaultsDevelopmentAndURLPolicy(t *testing.T) {
	t.Parallel()

	defaulted, err := normalizeOptions("google", Options{
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURI:  "https://app.example.com/oauth/callback",
		HTTPClient:   &http.Client{},
	}, googleDefaults)
	if err != nil {
		t.Fatalf("normalizeOptions(defaults) error = %v", err)
	}
	if defaulted.timeout != defaultTimeout ||
		defaulted.maxResponseBytes != defaultMaxResponseBytes ||
		defaulted.maxConcurrentRequests != defaultMaxConcurrentRequests ||
		defaulted.endpoints.ProfileURL != googleUserInfoURL {
		t.Fatalf("defaulted options = %#v", defaulted)
	}

	developmentDefaults := googleDefaults
	developmentDefaults.endpoints = Endpoints{
		AuthorizationURL: "http://127.0.0.1:8080/authorize",
		TokenURL:         "http://localhost:8080/token",
		ProfileURL:       "http://[::1]:8080/profile",
	}
	if _, err := normalizeOptions("google", Options{
		ClientID:                 "client",
		ClientSecret:             "secret",
		RedirectURI:              "http://localhost:3000/callback",
		HTTPClient:               &http.Client{},
		AllowInsecureDevelopment: true,
	}, developmentDefaults); err != nil {
		t.Fatalf("normalizeOptions(development) error = %v", err)
	}

	for _, raw := range []string{
		"http://example.com/callback",
		"https://user@example.com/callback",
		"https://example.com/callback#fragment",
	} {
		if err := validateAbsoluteURL(raw, true, true); !errors.Is(
			err,
			ErrInvalidConfiguration,
		) {
			t.Errorf("validateAbsoluteURL(%q) error = %v", raw, err)
		}
	}
	if err := validateAbsoluteURL(
		"https://provider.example.com/profile?secret=value",
		false,
		false,
	); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("endpoint query error = %v", err)
	}

	if err := validateScopes(
		[]string{"profile"},
		googleDefaults.allowedScopes,
		googleDefaults.requiredScopes,
	); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("missing required scope error = %v", err)
	}
	if err := validateScopes(
		[]string{"openid", "email", "profile", "extra"},
		googleDefaults.allowedScopes,
		googleDefaults.requiredScopes,
	); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("excess scope error = %v", err)
	}
}

func TestBoundedHTTPClientAndRoundTripper(t *testing.T) {
	t.Parallel()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	input := &http.Client{
		Transport: &http.Transport{MaxIdleConnsPerHost: 100},
		Timeout:   time.Minute,
		Jar:       jar,
	}
	client := boundedHTTPClient(input, time.Second, 2)
	if client == input ||
		client.Timeout != time.Second ||
		client.Jar != nil ||
		client.CheckRedirect == nil {
		t.Fatalf("boundedHTTPClient() = %#v", client)
	}
	bounded, ok := client.Transport.(*boundedRoundTripper)
	if !ok {
		t.Fatalf("transport = %T", client.Transport)
	}
	cloned, ok := bounded.base.(*http.Transport)
	if !ok || cloned == input.Transport || cloned.MaxConnsPerHost != 2 ||
		cloned.MaxIdleConnsPerHost != 2 {
		t.Fatalf("cloned transport = %#v", cloned)
	}
	redirectRequest, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if redirectErr := client.CheckRedirect(redirectRequest, nil); !errors.Is(
		redirectErr,
		http.ErrUseLastResponse,
	) {
		t.Fatalf("CheckRedirect() error = %v", redirectErr)
	}

	blocked := &boundedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("base transport called while slot was unavailable")
			return nil, nil
		}),
		slots: make(chan struct{}, 1),
	}
	blocked.slots <- struct{}{}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	request, err := http.NewRequestWithContext(cancelled, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.RoundTrip(request); !errors.Is(err, context.Canceled) {
		t.Fatalf("RoundTrip(cancelled) error = %v", err)
	}

	transportError := errors.New("transport failed")
	failing := &boundedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportError
		}),
		slots: make(chan struct{}, 1),
	}
	request, _ = http.NewRequest(http.MethodGet, "https://example.com", nil)
	if _, err := failing.RoundTrip(request); !errors.Is(err, transportError) {
		t.Fatalf("RoundTrip(failing) error = %v", err)
	}
}

func TestStrictJSONScannerAndReadErrors(t *testing.T) {
	t.Parallel()

	for _, data := range []string{
		`{"outer":{"duplicate":1,"duplicate":2}}`,
		`[{"ok":true},{"duplicate":1,"duplicate":2}]`,
		`{"ok":true}{"trailing":true}`,
		`{"unterminated":`,
	} {
		if err := validateUniqueObjectKeys([]byte(data)); err == nil {
			t.Errorf("validateUniqueObjectKeys(%q) error = nil", data)
		}
	}
	if err := validateUniqueObjectKeys([]byte(`[{"nested":{"ok":true}},2]`)); err != nil {
		t.Fatalf("validateUniqueObjectKeys(valid) error = %v", err)
	}
	closeAndDrain(nil)

	core := &providerCore{
		name:             "test",
		maxResponseBytes: 1024,
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(errorReader{}),
	}
	if err := core.decodeJSONResponse(
		context.Background(),
		response,
		&struct{}{},
		"profile",
	); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("decodeJSONResponse(read failure) error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	response.Body = io.NopCloser(errorReader{})
	if err := core.decodeJSONResponse(
		cancelled,
		response,
		&struct{}{},
		"profile",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("decodeJSONResponse(cancelled) error = %v", err)
	}
}

func TestSelectGitHubEmailPrecedence(t *testing.T) {
	t.Parallel()

	emails := []githubEmailResponse{
		{Email: "fallback@example.com", Verified: true},
		{Email: "primary@example.com", Primary: true, Verified: true},
	}
	if email, verified := selectGitHubEmail("public@example.com", emails); email !=
		"primary@example.com" || !verified {
		t.Fatalf("selectGitHubEmail(primary) = %q, %t", email, verified)
	}
	if email, verified := selectGitHubEmail(
		"public@example.com",
		emails[:1],
	); email != "fallback@example.com" || !verified {
		t.Fatalf("selectGitHubEmail(fallback) = %q, %t", email, verified)
	}
	if email, verified := selectGitHubEmail(
		"public@example.com",
		[]githubEmailResponse{{Email: "no@example.com"}},
	); email != "public@example.com" || verified {
		t.Fatalf("selectGitHubEmail(public) = %q, %t", email, verified)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
