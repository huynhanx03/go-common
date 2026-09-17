package oauth

import "context"

// AuthorizationRequest contains the request-bound values needed to construct a
// provider authorization URL. State persistence and PKCE verifier storage
// remain application responsibilities.
type AuthorizationRequest struct {
	State         string
	PKCEChallenge string
	RedirectURI   string
}

// ExchangeRequest contains a one-time provider code and its matching PKCE
// verifier. Implementations never retain or return either secret.
type ExchangeRequest struct {
	Code         string
	PKCEVerifier string
	RedirectURI  string
}

// Profile is the bounded provider-neutral identity returned after exchange.
// Subject is the provider's immutable identifier; email is never an identity
// key.
type Profile struct {
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
	Login         string
	AvatarURL     string
}

// Provider is the stable, PKCE-enforcing OAuth provider contract.
type Provider interface {
	Name() string
	AuthorizationURL(context.Context, AuthorizationRequest) (string, error)
	Exchange(context.Context, ExchangeRequest) (Profile, error)
}

// OAuthUserInfo is retained only for compatibility with migrating consumers.
//
// Deprecated: use Profile.
type OAuthUserInfo struct {
	ExternalID string
	Metadata   map[string]any
}

// LegacyProvider is the temporary state-only compatibility surface.
//
// Deprecated: migrate to Provider and application-owned PKCE state.
type LegacyProvider interface {
	Name() string
	AuthCodeURL(state string) string
	ExchangeCode(context.Context, string) (*OAuthUserInfo, error)
}
