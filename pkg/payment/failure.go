package payment

import "github.com/huynhanx03/go-common/pkg/common/apperr"

// Business codes this package owns. They sit inside go-common's generic block
// (module digit 0) and inherit their HTTP status from the code range via
// response.GetHTTPCode — no registration needed:
//
//   - 400xx -> 400 Bad Request
//   - 500xx -> 500 Internal Server Error
const (
	// CodePaymentDeclined is a payment the provider refused for a reason the
	// customer can usually act on (declined card, insufficient funds, ...).
	CodePaymentDeclined = 40010
	// CodePaymentInvalid is a request the provider rejected as malformed or
	// referencing something that does not exist.
	CodePaymentInvalid = 40011
	// CodePaymentProviderError is an upstream or unexpected provider failure.
	CodePaymentProviderError = 50005
)

// FailureCode is the neutral classification every provider maps its errors to,
// so a consumer branches on one vocabulary regardless of which provider raised
// the failure.
type FailureCode string

const (
	FailureUnknown                FailureCode = "unknown"
	FailureCardDeclined           FailureCode = "card_declined"
	FailureInsufficientFunds      FailureCode = "insufficient_funds"
	FailureExpiredCard            FailureCode = "expired_card"
	FailureAuthenticationRequired FailureCode = "authentication_required"
	FailureFraudulent             FailureCode = "fraudulent"
	FailureCurrencyNotSupported   FailureCode = "currency_not_supported"
	FailureInvalidRequest         FailureCode = "invalid_request"
	FailureResourceMissing        FailureCode = "resource_missing"
	FailureRateLimited            FailureCode = "rate_limited"
	FailureProcessingError        FailureCode = "processing_error"
	FailureProviderUnavailable    FailureCode = "provider_unavailable"
)

// Failure is a provider-neutral payment failure. Code is the neutral
// classification for programmatic handling; Message is client-safe copy; the
// Provider* fields keep the raw codes for logs and debugging; Retryable hints
// whether an identical retry might succeed. Cause wraps the underlying error.
type Failure struct {
	Code                FailureCode
	Message             string
	ProviderCode        string
	ProviderDeclineCode string
	Retryable           bool
	Cause               error
}

// appErrCode buckets the neutral FailureCode into the HTTP-facing business code.
func (f *Failure) appErrCode() int {
	switch f.Code {
	case FailureCardDeclined,
		FailureInsufficientFunds,
		FailureExpiredCard,
		FailureAuthenticationRequired,
		FailureFraudulent,
		FailureCurrencyNotSupported:
		return CodePaymentDeclined
	case FailureInvalidRequest:
		return CodePaymentInvalid
	case FailureResourceMissing:
		return apperr.CodeNotFound
	case FailureRateLimited:
		return apperr.CodeTooManyRequests
	default:
		return CodePaymentProviderError
	}
}

// ToAppError renders the failure as go-common's *apperr.AppError, mapping the
// neutral code to the right HTTP status and preserving the cause for the error
// chain. A client-safe Message is used when present.
func (f *Failure) ToAppError() *apperr.AppError {
	if f == nil {
		return apperr.New(CodePaymentProviderError, "payment failed", nil)
	}

	message := f.Message
	if message == "" {
		message = "payment failed"
	}

	return apperr.New(f.appErrCode(), message, f.Cause)
}

// Invalid builds an *apperr.AppError for a request this framework rejects before
// it reaches the provider (e.g. a nil request struct).
func Invalid(message string) *apperr.AppError {
	return apperr.New(CodePaymentInvalid, message, nil)
}

// ProviderContractBroken builds an *apperr.AppError for a provider that reported
// success but returned no object, which no caller can recover from.
func ProviderContractBroken(message string) *apperr.AppError {
	return apperr.New(CodePaymentProviderError, message, nil)
}
