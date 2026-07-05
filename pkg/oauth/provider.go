package oauth

import "context"

// OAuthUserInfo represents the minimal, standardized identity from any OAuth provider.
type OAuthUserInfo struct {
	// ExternalID is the unique identifier for the user within the provider (e.g., 'sub' in Google, 'id' in GitHub).
	ExternalID string

	// Metadata contains all other provider-specific claims (email, name, picture, verified, etc.).
	Metadata map[string]any
}

// Provider defines the interface that all OAuth providers must implement.
type Provider interface {
	// AuthCodeURL returns the provider authorization URL for a state value.
	AuthCodeURL(state string) string

	// ExchangeCode exchanges an authorization code for standardized user info.
	ExchangeCode(ctx context.Context, code string) (*OAuthUserInfo, error)

	// Name returns the provider identifier (e.g. "google", "github").
	Name() string
}
