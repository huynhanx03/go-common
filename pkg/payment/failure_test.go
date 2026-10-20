package payment

import (
	"errors"
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

func TestFailureToAppErrorMapsCodeAndHTTPStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code     FailureCode
		wantCode int
		wantHTTP int
	}{
		{FailureCardDeclined, CodePaymentDeclined, 400},
		{FailureInsufficientFunds, CodePaymentDeclined, 400},
		{FailureExpiredCard, CodePaymentDeclined, 400},
		{FailureAuthenticationRequired, CodePaymentDeclined, 400},
		{FailureFraudulent, CodePaymentDeclined, 400},
		{FailureCurrencyNotSupported, CodePaymentDeclined, 400},
		{FailureInvalidRequest, CodePaymentInvalid, 400},
		{FailureResourceMissing, apperr.CodeNotFound, 404},
		{FailureRateLimited, apperr.CodeTooManyRequests, 429},
		{FailureProcessingError, CodePaymentProviderError, 500},
		{FailureProviderUnavailable, CodePaymentProviderError, 500},
		{FailureUnknown, CodePaymentProviderError, 500},
	}

	for _, c := range cases {
		appErr := (&Failure{Code: c.code, Message: "boom"}).ToAppError()

		if appErr.Code != c.wantCode {
			t.Errorf("%s -> code %d, want %d", c.code, appErr.Code, c.wantCode)
		}

		if got := response.GetHTTPCode(appErr.Code); got != c.wantHTTP {
			t.Errorf("%s -> HTTP %d, want %d", c.code, got, c.wantHTTP)
		}
	}
}

func TestFailureToAppErrorPreservesCauseAndMessage(t *testing.T) {
	t.Parallel()

	cause := errors.New("upstream boom")
	appErr := (&Failure{Code: FailureCardDeclined, Message: "declined", Cause: cause}).ToAppError()

	if appErr.Message != "declined" {
		t.Errorf("message = %q", appErr.Message)
	}

	if !errors.Is(appErr, cause) {
		t.Error("cause must be reachable through the error chain")
	}
}

func TestFailureToAppErrorDefaultsMessage(t *testing.T) {
	t.Parallel()

	if got := (&Failure{Code: FailureUnknown}).ToAppError().Message; got != "payment failed" {
		t.Errorf("default message = %q", got)
	}

	if got := (*Failure)(nil).ToAppError(); got.Code != CodePaymentProviderError {
		t.Errorf("nil failure code = %d", got.Code)
	}
}
