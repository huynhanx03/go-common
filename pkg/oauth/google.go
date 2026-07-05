package oauth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
)

const (
	googleUserInfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"
	httpTimeout       = 10 * time.Second
)

type googleUserInfoResp struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// GoogleProvider implements the Provider interface for Google OAuth/OIDC.
type GoogleProvider struct {
	config *oauth2.Config
}

// NewGoogleProvider creates a new Google OAuth provider.
func NewGoogleProvider(clientID, clientSecret, redirectURL string) *GoogleProvider {
	return &GoogleProvider{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
	}
}

func (g *GoogleProvider) AuthCodeURL(state string) string {
	return g.config.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (g *GoogleProvider) Name() string {
	return "google"
}

func (g *GoogleProvider) ExchangeCode(ctx context.Context, code string) (*OAuthUserInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	token, err := g.config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange google code: %w", err)
	}

	client := g.config.Client(ctx, token)
	resp, err := client.Get(googleUserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("get google userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google userinfo returned %d", resp.StatusCode)
	}

	var raw googleUserInfoResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode google userinfo: %w", err)
	}
	if raw.Sub == "" {
		return nil, fmt.Errorf("google userinfo missing sub")
	}

	return &OAuthUserInfo{
		ExternalID: raw.Sub,
		Metadata: map[string]any{
			"email":    raw.Email,
			"verified": raw.EmailVerified,
			"name":     raw.Name,
			"picture":  raw.Picture,
		},
	}, nil
}
