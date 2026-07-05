package constraints

type ContextKey string

const (
	HeaderAuthorization = "Authorization"
	TokenTypeBearer     = "Bearer"

	ContextKeyUserID   ContextKey = "user_id"
	ContextKeyUsername ContextKey = "username"
	ContextKeyClaims   ContextKey = "claims"
)
