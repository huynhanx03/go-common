package oauth

import (
	"context"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const googleUserInfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"

var googleDefaults = providerDefaults{
	endpoints: Endpoints{
		AuthorizationURL: google.Endpoint.AuthURL,
		TokenURL:         google.Endpoint.TokenURL,
		ProfileURL:       googleUserInfoURL,
	},
	scopes: []string{"openid", "email", "profile"},
	allowedScopes: map[string]struct{}{
		"openid":  {},
		"email":   {},
		"profile": {},
	},
	requiredScopes: []string{"openid"},
	authStyle:      oauth2.AuthStyleInParams,
}

type googleUserInfoResponse struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
	Locale        string `json:"locale"`
	HostedDomain  string `json:"hd"`
}

// GoogleProvider implements the bounded Google OAuth/OIDC adapter.
type GoogleProvider struct {
	core       *providerCore
	profileURL string
}

// NewGoogleProvider validates and snapshots a Google provider configuration.
func NewGoogleProvider(options Options) (*GoogleProvider, error) {
	normalized, err := normalizeOptions("google", options, googleDefaults)
	if err != nil {
		return nil, err
	}
	return &GoogleProvider{
		core:       newProviderCore("google", normalized),
		profileURL: normalized.endpoints.ProfileURL,
	}, nil
}

func (g *GoogleProvider) Name() string {
	return "google"
}

func (g *GoogleProvider) AuthorizationURL(
	ctx context.Context,
	request AuthorizationRequest,
) (string, error) {
	return g.core.authorizationURL(ctx, request)
}

func (g *GoogleProvider) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Profile, error) {
	return g.exchange(ctx, request, true)
}

func (g *GoogleProvider) exchange(
	ctx context.Context,
	request ExchangeRequest,
	requirePKCE bool,
) (Profile, error) {
	exchangeContext, cancel, err := g.core.startExchange(ctx, request, requirePKCE)
	if err != nil {
		return Profile{}, err
	}
	defer cancel()

	accessToken, err := g.core.exchangeToken(exchangeContext, request, requirePKCE)
	if err != nil {
		return Profile{}, err
	}
	var raw googleUserInfoResponse
	if err := g.core.getJSON(
		exchangeContext,
		g.profileURL,
		accessToken,
		&raw,
		"profile",
	); err != nil {
		return Profile{}, err
	}
	return normalizeProfile(g.Name(), Profile{
		Subject:       raw.Subject,
		Email:         raw.Email,
		EmailVerified: raw.EmailVerified,
		DisplayName:   raw.Name,
		AvatarURL:     raw.Picture,
	})
}

// AuthCodeURL is a state-only compatibility wrapper.
//
// Deprecated: use AuthorizationURL with PKCE.
func (g *GoogleProvider) AuthCodeURL(state string) string {
	return g.core.legacyAuthorizationURL(state)
}

// ExchangeCode is a non-PKCE compatibility wrapper.
//
// Deprecated: use Exchange with a matching PKCE verifier.
func (g *GoogleProvider) ExchangeCode(
	ctx context.Context,
	code string,
) (*OAuthUserInfo, error) {
	request, err := g.core.legacyExchangeRequest(code)
	if err != nil {
		return nil, err
	}
	profile, err := g.exchange(ctx, request, false)
	if err != nil {
		return nil, err
	}
	return legacyUserInfo(profile), nil
}

var (
	_ Provider       = (*GoogleProvider)(nil)
	_ LegacyProvider = (*GoogleProvider)(nil)
)
