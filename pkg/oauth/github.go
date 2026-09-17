package oauth

import (
	"context"
	"encoding/json"
	"strconv"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

const (
	githubUserURL   = "https://api.github.com/user"
	githubEmailsURL = "https://api.github.com/user/emails"
)

var githubDefaults = providerDefaults{
	endpoints: Endpoints{
		AuthorizationURL: github.Endpoint.AuthURL,
		TokenURL:         github.Endpoint.TokenURL,
		ProfileURL:       githubUserURL,
		EmailsURL:        githubEmailsURL,
	},
	scopes: []string{"read:user", "user:email"},
	allowedScopes: map[string]struct{}{
		"read:user":  {},
		"user:email": {},
	},
	requiredScopes: []string{"read:user"},
	authStyle:      oauth2.AuthStyleInParams,
	requireEmails:  true,
}

type githubUserResponse struct {
	Login                   string          `json:"login"`
	ID                      int64           `json:"id"`
	NodeID                  string          `json:"node_id"`
	AvatarURL               string          `json:"avatar_url"`
	GravatarID              string          `json:"gravatar_id"`
	URL                     string          `json:"url"`
	HTMLURL                 string          `json:"html_url"`
	FollowersURL            string          `json:"followers_url"`
	FollowingURL            string          `json:"following_url"`
	GistsURL                string          `json:"gists_url"`
	StarredURL              string          `json:"starred_url"`
	SubscriptionsURL        string          `json:"subscriptions_url"`
	OrganizationsURL        string          `json:"organizations_url"`
	ReposURL                string          `json:"repos_url"`
	EventsURL               string          `json:"events_url"`
	ReceivedEventsURL       string          `json:"received_events_url"`
	Type                    string          `json:"type"`
	UserViewType            string          `json:"user_view_type"`
	SiteAdmin               bool            `json:"site_admin"`
	Name                    string          `json:"name"`
	Company                 string          `json:"company"`
	Blog                    string          `json:"blog"`
	Location                string          `json:"location"`
	Email                   string          `json:"email"`
	Hireable                *bool           `json:"hireable"`
	Bio                     string          `json:"bio"`
	TwitterUsername         string          `json:"twitter_username"`
	NotificationEmail       string          `json:"notification_email"`
	PublicRepos             int64           `json:"public_repos"`
	PublicGists             int64           `json:"public_gists"`
	Followers               int64           `json:"followers"`
	Following               int64           `json:"following"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
	PrivateGists            int64           `json:"private_gists"`
	TotalPrivateRepos       int64           `json:"total_private_repos"`
	OwnedPrivateRepos       int64           `json:"owned_private_repos"`
	DiskUsage               int64           `json:"disk_usage"`
	Collaborators           int64           `json:"collaborators"`
	TwoFactorAuthentication bool            `json:"two_factor_authentication"`
	Plan                    json.RawMessage `json:"plan"`
}

type githubEmailResponse struct {
	Email      string `json:"email"`
	Primary    bool   `json:"primary"`
	Verified   bool   `json:"verified"`
	Visibility string `json:"visibility"`
}

// GitHubProvider implements the bounded GitHub OAuth adapter.
type GitHubProvider struct {
	core       *providerCore
	profileURL string
	emailsURL  string
}

// NewGitHubProvider validates and snapshots a GitHub provider configuration.
func NewGitHubProvider(options Options) (*GitHubProvider, error) {
	normalized, err := normalizeOptions("github", options, githubDefaults)
	if err != nil {
		return nil, err
	}
	return &GitHubProvider{
		core:       newProviderCore("github", normalized),
		profileURL: normalized.endpoints.ProfileURL,
		emailsURL:  normalized.endpoints.EmailsURL,
	}, nil
}

func (g *GitHubProvider) Name() string {
	return "github"
}

func (g *GitHubProvider) AuthorizationURL(
	ctx context.Context,
	request AuthorizationRequest,
) (string, error) {
	return g.core.authorizationURL(ctx, request)
}

func (g *GitHubProvider) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Profile, error) {
	return g.exchange(ctx, request, true)
}

func (g *GitHubProvider) exchange(
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
	var user githubUserResponse
	if err := g.core.getJSON(
		exchangeContext,
		g.profileURL,
		accessToken,
		&user,
		"profile",
	); err != nil {
		return Profile{}, err
	}
	if user.ID <= 0 {
		return Profile{}, newProviderError(ErrMissingSubject, g.Name(), "profile")
	}

	var emails []githubEmailResponse
	if err := g.core.getJSON(
		exchangeContext,
		g.emailsURL,
		accessToken,
		&emails,
		"emails",
	); err != nil {
		return Profile{}, err
	}
	email, verified := selectGitHubEmail(user.Email, emails)
	return normalizeProfile(g.Name(), Profile{
		Subject:       strconv.FormatInt(user.ID, 10),
		Email:         email,
		EmailVerified: verified,
		DisplayName:   user.Name,
		Login:         user.Login,
		AvatarURL:     user.AvatarURL,
	})
}

func selectGitHubEmail(
	publicEmail string,
	emails []githubEmailResponse,
) (string, bool) {
	for _, email := range emails {
		if email.Primary && email.Verified && email.Email != "" {
			return email.Email, true
		}
	}
	for _, email := range emails {
		if email.Verified && email.Email != "" {
			return email.Email, true
		}
	}
	return publicEmail, false
}

// AuthCodeURL is a state-only compatibility wrapper.
//
// Deprecated: use AuthorizationURL with PKCE.
func (g *GitHubProvider) AuthCodeURL(state string) string {
	return g.core.legacyAuthorizationURL(state)
}

// ExchangeCode is a non-PKCE compatibility wrapper.
//
// Deprecated: use Exchange with a matching PKCE verifier.
func (g *GitHubProvider) ExchangeCode(
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
	_ Provider       = (*GitHubProvider)(nil)
	_ LegacyProvider = (*GitHubProvider)(nil)
)
