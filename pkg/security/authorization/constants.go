package authorization

const (
	maxResourceBytes   = 256
	maxResourceIDBytes = 256
	maxActionBytes     = 128
	maxScopeTypeBytes  = 64
	maxScopeIDBytes    = 256

	defaultSystemScopeType = "system"
	defaultSystemScopeID   = "default"
	scopeKeySeparator      = ":"

	ReasonAllowed         = "allowed"
	ReasonPolicyDenied    = "policy_denied"
	ReasonAnonymous       = "anonymous"
	ReasonInvalidRequest  = "invalid_request"
	ReasonEvaluationError = "evaluation_error"
)
