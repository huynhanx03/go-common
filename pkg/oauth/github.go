package oauth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

const (
	githubUserURL   = "https://api.github.com/user"
	githubEmailsURL = "https://api.github.com/user/emails"
)

type GitHubProvider struct {
	config *oauth2.Config
}

type githubUserResp struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

type githubEmailResp struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func NewGitHubProvider(clientID, clientSecret, redirectURL string) *GitHubProvider {
	return &GitHubProvider{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"read:user", "user:email"},
			Endpoint:     github.Endpoint,
		},
	}
}

func (g *GitHubProvider) AuthCodeURL(state string) string {
	return g.config.AuthCodeURL(state)
}

func (g *GitHubProvider) Name() string {
	return "github"
}

func (g *GitHubProvider) ExchangeCode(ctx context.Context, code string) (*OAuthUserInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	token, err := g.config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange github code: %w", err)
	}

	client := g.config.Client(ctx, token)
	raw, err := getJSON[githubUserResp](client, githubUserURL)
	if err != nil {
		return nil, err
	}
	if raw.ID == 0 {
		return nil, fmt.Errorf("github userinfo missing id")
	}

	email := raw.Email
	verified := email != ""
	if email == "" {
		email, verified = selectGitHubEmail(client)
	}

	return &OAuthUserInfo{
		ExternalID: strconv.FormatInt(raw.ID, 10),
		Metadata: map[string]any{
			"email":      email,
			"verified":   verified,
			"name":       raw.Name,
			"login":      raw.Login,
			"avatar_url": raw.AvatarURL,
		},
	}, nil
}

func selectGitHubEmail(client *http.Client) (string, bool) {
	emails, err := getJSON[[]githubEmailResp](client, githubEmailsURL)
	if err != nil {
		return "", false
	}

	for _, item := range emails {
		if item.Primary && item.Verified {
			return item.Email, true
		}
	}
	for _, item := range emails {
		if item.Verified {
			return item.Email, true
		}
	}
	return "", false
}

func getJSON[T any](client *http.Client, url string) (T, error) {
	var out T
	resp, err := client.Get(url)
	if err != nil {
		return out, fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("decode %s: %w", url, err)
	}
	return out, nil
}
