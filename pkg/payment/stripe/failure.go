package stripe

import (
	"errors"
	"strings"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
	"go.uber.org/zap"
)

const genericCardFailure = "An error occurred while processing your card. Try again in a little bit."

// fail logs the underlying error and returns the classified, client-safe
// *apperr.AppError. Every adapter method funnels provider errors through here so
// the raw cause is logged once and never leaks to the caller.
func (p *Provider) fail(logMsg string, err error) *apperr.AppError {
	p.logError(logMsg, err)

	return failureFromError(err).ToAppError()
}

func (p *Provider) logError(msg string, err error) {
	if p.logger == nil {
		return
	}

	p.logger.Error(msg, zap.Error(err))
}

func (p *Provider) logWarn(msg string, err error) {
	if p.logger == nil {
		return
	}

	p.logger.Warn(msg, zap.Error(err))
}

// failureFromError classifies a Stripe error into a neutral payment.Failure,
// preserving the raw provider codes and choosing a client-safe message.
func failureFromError(err error) *payment.Failure {
	var sdkErr *stripesdk.Error
	if !errors.As(err, &sdkErr) {
		return &payment.Failure{
			Code:      payment.FailureProviderUnavailable,
			Message:   genericCardFailure,
			Retryable: true,
			Cause:     err,
		}
	}

	declineCode := strings.ToLower(strings.TrimSpace(string(sdkErr.DeclineCode)))
	code := strings.ToLower(strings.TrimSpace(string(sdkErr.Code)))

	failureCode := classify(declineCode, code, sdkErr.Type)

	return &payment.Failure{
		Code:                failureCode,
		Message:             failureMessage(declineCode, code),
		ProviderCode:        string(sdkErr.Code),
		ProviderDeclineCode: string(sdkErr.DeclineCode),
		Retryable:           retryable(failureCode),
		Cause:               err,
	}
}

// classify prefers the decline code (most specific), then the error code, then
// the coarse error type.
func classify(declineCode, code string, errType stripesdk.ErrorType) payment.FailureCode {
	if fc, ok := declineCodeFailures[declineCode]; ok {
		return fc
	}

	if fc, ok := errorCodeFailures[code]; ok {
		return fc
	}

	switch errType {
	case stripesdk.ErrorTypeRateLimit:
		return payment.FailureRateLimited
	case stripesdk.ErrorTypeInvalidRequest, stripesdk.ErrorTypeIdempotency:
		return payment.FailureInvalidRequest
	case stripesdk.ErrorTypeCard:
		return payment.FailureCardDeclined
	case stripesdk.ErrorTypeAPI:
		return payment.FailureProviderUnavailable
	default:
		return payment.FailureUnknown
	}
}

func retryable(fc payment.FailureCode) bool {
	switch fc {
	case payment.FailureRateLimited,
		payment.FailureProcessingError,
		payment.FailureProviderUnavailable:
		return true
	default:
		return false
	}
}

func failureMessage(declineCode, code string) string {
	if msg, ok := declineCodeMessages[declineCode]; ok {
		return msg
	}

	if msg, ok := errorCodeMessages[code]; ok {
		return msg
	}

	return genericCardFailure
}

var declineCodeFailures = map[string]payment.FailureCode{
	"insufficient_funds":       payment.FailureInsufficientFunds,
	"lost_card":                payment.FailureCardDeclined,
	"stolen_card":              payment.FailureCardDeclined,
	"authentication_required":  payment.FailureAuthenticationRequired,
	"issuer_not_available":     payment.FailureProviderUnavailable,
	"transaction_not_allowed":  payment.FailureCardDeclined,
	"fraudulent":               payment.FailureFraudulent,
	"merchant_blacklist":       payment.FailureFraudulent,
	"generic_decline":          payment.FailureCardDeclined,
	"currency_not_supported":   payment.FailureCurrencyNotSupported,
	"expired_card":             payment.FailureExpiredCard,
	"rate_limit":               payment.FailureRateLimited,
	"billing_address_required": payment.FailureInvalidRequest,
	"parameter_invalid_empty":  payment.FailureInvalidRequest,
}

var errorCodeFailures = map[string]payment.FailureCode{
	"insufficient_funds":      payment.FailureInsufficientFunds,
	"authentication_required": payment.FailureAuthenticationRequired,
	"issuer_not_available":    payment.FailureProviderUnavailable,
	"rate_limit":              payment.FailureRateLimited,
	"resource_missing":        payment.FailureResourceMissing,
	"processing_error":        payment.FailureProcessingError,
	"expired_card":            payment.FailureExpiredCard,
	"card_declined":           payment.FailureCardDeclined,
	"parameter_invalid_empty": payment.FailureInvalidRequest,
}

var declineCodeMessages = map[string]string{
	"insufficient_funds":       "Your card has insufficient funds.",
	"lost_card":                "Your card has been declined.",
	"stolen_card":              "Your card has been declined.",
	"authentication_required":  "Your card was declined. This transaction requires authentication.",
	"issuer_not_available":     "Your card issuer is unavailable. Try again later.",
	"transaction_not_allowed":  "Your card does not support this type of purchase.",
	"fraudulent":               "Your card has been declined.",
	"generic_decline":          "Your card was declined.",
	"currency_not_supported":   "Your card does not support the specified currency.",
	"merchant_blacklist":       "Your card was declined.",
	"rate_limit":               "Failed due to a system error, please try again or contact support.",
	"billing_address_required": "Your billing address is required to complete this payment.",
	"parameter_invalid_empty":  "The payment request was invalid. Please try again or contact support.",
}

var errorCodeMessages = map[string]string{
	"insufficient_funds":      "Your card has insufficient funds.",
	"authentication_required": "Your card was declined. This transaction requires authentication.",
	"issuer_not_available":    "Your card issuer is unavailable. Try again later.",
	"rate_limit":              "Failed due to a system error, please try again or contact support.",
	"resource_missing":        "A required billing resource was not found. Please try again or contact support.",
	"processing_error":        genericCardFailure,
	"parameter_invalid_empty": "The payment request was invalid. Please try again or contact support.",
}
