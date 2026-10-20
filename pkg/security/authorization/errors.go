package authorization

import "errors"

var (
	ErrInvalidRequest = errors.New("authorization: invalid request")
	ErrEvaluation     = errors.New("authorization: evaluation failed")
)

const (
	ReasonAllowed         = "allowed"
	ReasonPolicyDenied    = "policy_denied"
	ReasonAnonymous       = "anonymous"
	ReasonInvalidRequest  = "invalid_request"
	ReasonEvaluationError = "evaluation_error"
)
