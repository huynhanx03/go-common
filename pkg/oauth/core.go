package oauth

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/oauth2"
)

const (
	maxStateBytes     = 512
	maxCodeBytes      = 2048
	minVerifierBytes  = 43
	maxVerifierBytes  = 128
	challengeBytes    = 43
	maxSubjectBytes   = 256
	maxEmailBytes     = 320
	maxDisplayBytes   = 256
	maxLoginBytes     = 128
	maxAvatarURLBytes = 2048
	maxAccessToken    = 8192
)

type providerCore struct {
	name                string
	config              oauth2.Config
	httpClient          *http.Client
	timeout             time.Duration
	maxResponseBytes    int64
	redirectURI         string
	enableCompatibility bool
	tokenURL            string
	clientID            string
	clientSecret        string
	authStyle           oauth2.AuthStyle
}

func newProviderCore(name string, options normalizedOptions) *providerCore {
	return &providerCore{
		name: name,
		config: oauth2.Config{
			ClientID:     options.clientID,
			ClientSecret: options.clientSecret,
			RedirectURL:  options.redirectURI,
			Scopes:       append([]string(nil), options.scopes...),
			Endpoint: oauth2.Endpoint{
				AuthURL:   options.endpoints.AuthorizationURL,
				TokenURL:  options.endpoints.TokenURL,
				AuthStyle: options.authStyle,
			},
		},
		httpClient:          options.httpClient,
		timeout:             options.timeout,
		maxResponseBytes:    options.maxResponseBytes,
		redirectURI:         options.redirectURI,
		enableCompatibility: options.enableCompatibility,
		tokenURL:            options.endpoints.TokenURL,
		clientID:            options.clientID,
		clientSecret:        options.clientSecret,
		authStyle:           options.authStyle,
	}
}

func (p *providerCore) authorizationURL(
	ctx context.Context,
	request AuthorizationRequest,
) (string, error) {
	if err := validateContext(ctx); err != nil {
		return "", err
	}
	if err := validateRequestValue(request.State, 1, maxStateBytes, false); err != nil ||
		!validChallenge(request.PKCEChallenge) ||
		request.RedirectURI != p.redirectURI {
		return "", newProviderError(ErrInvalidRequest, p.name, "authorize")
	}
	result := p.config.AuthCodeURL(
		request.State,
		oauth2.SetAuthURLParam("code_challenge", request.PKCEChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return result, nil
}

func (p *providerCore) startExchange(
	ctx context.Context,
	request ExchangeRequest,
	requirePKCE bool,
) (context.Context, context.CancelFunc, error) {
	if err := validateContext(ctx); err != nil {
		return nil, nil, err
	}
	if err := validateRequestValue(request.Code, 1, maxCodeBytes, false); err != nil ||
		request.RedirectURI != p.redirectURI ||
		(requirePKCE && !validVerifier(request.PKCEVerifier)) ||
		(!requirePKCE && request.PKCEVerifier != "") {
		return nil, nil, newProviderError(ErrInvalidRequest, p.name, "exchange")
	}
	exchangeContext, cancel := context.WithTimeout(ctx, p.timeout)
	return exchangeContext, cancel, nil
}

func (p *providerCore) exchangeToken(
	ctx context.Context,
	request ExchangeRequest,
	requirePKCE bool,
) (string, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {request.Code},
		"redirect_uri": {request.RedirectURI},
	}
	if requirePKCE {
		form.Set("code_verifier", request.PKCEVerifier)
	}
	if p.authStyle == oauth2.AuthStyleInParams {
		form.Set("client_id", p.clientID)
		form.Set("client_secret", p.clientSecret)
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.tokenURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", newProviderError(ErrInvalidConfiguration, p.name, "token")
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.authStyle != oauth2.AuthStyleInParams {
		httpRequest.SetBasicAuth(p.clientID, p.clientSecret)
	}
	response, err := p.httpClient.Do(httpRequest)
	if err != nil {
		return "", p.transportError(ctx, "token", err)
	}
	defer closeAndDrain(response.Body)
	if response.StatusCode != http.StatusOK {
		return "", p.statusError("token", response.StatusCode)
	}
	var raw tokenResponse
	if err := p.decodeJSONResponse(ctx, response, &raw, "token"); err != nil {
		return "", err
	}
	if validateRequestValue(raw.AccessToken, 1, maxAccessToken, false) != nil {
		return "", newProviderError(ErrMalformedResponse, p.name, "token")
	}
	return raw.AccessToken, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
}

func (p *providerCore) statusError(operation string, status int) error {
	switch {
	case status == http.StatusTooManyRequests:
		return newProviderError(ErrRateLimited, p.name, operation)
	case status >= 500:
		return newProviderError(ErrProviderUnavailable, p.name, operation)
	case status >= 400:
		return newProviderError(ErrExchangeRejected, p.name, operation)
	default:
		return newProviderError(ErrMalformedResponse, p.name, operation)
	}
}

func (p *providerCore) legacyAuthorizationURL(state string) string {
	if !p.enableCompatibility ||
		validateRequestValue(state, 1, maxStateBytes, false) != nil {
		return ""
	}
	return p.config.AuthCodeURL(state)
}

func (p *providerCore) legacyExchangeRequest(code string) (ExchangeRequest, error) {
	if !p.enableCompatibility {
		return ExchangeRequest{}, newProviderError(
			ErrCompatibilityDisabled,
			p.name,
			"legacy exchange",
		)
	}
	return ExchangeRequest{Code: code, RedirectURI: p.redirectURI}, nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidRequest
	}
	return ctx.Err()
}

func validateRequestValue(value string, minimum, maximum int, allowSpace bool) error {
	if len(value) < minimum || len(value) > maximum || strings.TrimSpace(value) != value {
		return ErrInvalidRequest
	}
	for _, character := range value {
		if unicode.IsControl(character) || (!allowSpace && unicode.IsSpace(character)) {
			return ErrInvalidRequest
		}
	}
	return nil
}

func validChallenge(value string) bool {
	if len(value) != challengeBytes {
		return false
	}
	for _, character := range value {
		if !isAlphaNumeric(character) && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func validVerifier(value string) bool {
	if len(value) < minVerifierBytes || len(value) > maxVerifierBytes {
		return false
	}
	for _, character := range value {
		if !isAlphaNumeric(character) &&
			character != '-' &&
			character != '.' &&
			character != '_' &&
			character != '~' {
			return false
		}
	}
	return true
}

func isAlphaNumeric(value rune) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func normalizeProfile(provider string, profile Profile) (Profile, error) {
	for _, value := range []string{
		profile.Subject,
		profile.Email,
		profile.DisplayName,
		profile.Login,
		profile.AvatarURL,
	} {
		for _, character := range value {
			if unicode.IsControl(character) {
				return Profile{}, newProviderError(
					ErrMalformedResponse,
					provider,
					"profile",
				)
			}
		}
	}
	profile.Subject = strings.TrimSpace(profile.Subject)
	profile.Email = strings.TrimSpace(profile.Email)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.Login = strings.TrimSpace(profile.Login)
	profile.AvatarURL = strings.TrimSpace(profile.AvatarURL)
	if profile.Subject == "" {
		return Profile{}, newProviderError(ErrMissingSubject, provider, "profile")
	}
	for _, field := range []struct {
		value      string
		maximum    int
		allowSpace bool
	}{
		{value: profile.Subject, maximum: maxSubjectBytes},
		{value: profile.Email, maximum: maxEmailBytes},
		{value: profile.DisplayName, maximum: maxDisplayBytes, allowSpace: true},
		{value: profile.Login, maximum: maxLoginBytes},
		{value: profile.AvatarURL, maximum: maxAvatarURLBytes},
	} {
		if field.value == "" {
			continue
		}
		if len(field.value) > field.maximum {
			return Profile{}, newProviderError(ErrMalformedResponse, provider, "profile")
		}
		if !field.allowSpace {
			for _, character := range field.value {
				if unicode.IsSpace(character) {
					return Profile{}, newProviderError(
						ErrMalformedResponse,
						provider,
						"profile",
					)
				}
			}
		}
	}
	if profile.Email != "" {
		address, err := mail.ParseAddress(profile.Email)
		if err != nil || address.Address != profile.Email {
			return Profile{}, newProviderError(
				ErrMalformedResponse,
				provider,
				"profile",
			)
		}
	}
	if profile.Email == "" {
		profile.EmailVerified = false
	}
	if profile.AvatarURL != "" {
		if err := validateAbsoluteURL(profile.AvatarURL, false, true); err != nil {
			return Profile{}, newProviderError(ErrMalformedResponse, provider, "profile")
		}
	}
	return profile, nil
}

func legacyUserInfo(profile Profile) *OAuthUserInfo {
	return &OAuthUserInfo{
		ExternalID: profile.Subject,
		Metadata: map[string]any{
			"email":      profile.Email,
			"verified":   profile.EmailVerified,
			"name":       profile.DisplayName,
			"login":      profile.Login,
			"picture":    profile.AvatarURL,
			"avatar_url": profile.AvatarURL,
		},
	}
}
