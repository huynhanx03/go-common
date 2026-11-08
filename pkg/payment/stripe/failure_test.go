package stripe

import (
	"errors"
	"fmt"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v87"
)

func TestFailureFromErrorClassifiesProviderFailures(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  payment.FailureCode
		wantRetry bool
		wantRaw   bool
	}{
		{
			name: "decline code has precedence over provider code and type",
			err: fmt.Errorf("wrapped: %w", &stripesdk.Error{
				Code:        stripesdk.ErrorCodeRateLimit,
				DeclineCode: stripesdk.DeclineCodeInsufficientFunds,
				Type:        stripesdk.ErrorTypeAPI,
				Msg:         "private provider detail",
			}),
			wantCode:  payment.FailureInsufficientFunds,
			wantRetry: false,
			wantRaw:   true,
		},
		{
			name: "provider code classifies a missing resource",
			err: &stripesdk.Error{
				Code: stripesdk.ErrorCodeResourceMissing,
				Type: stripesdk.ErrorTypeInvalidRequest,
			},
			wantCode: payment.FailureResourceMissing,
		},
		{
			name: "rate limit is retryable",
			err: &stripesdk.Error{
				Type: stripesdk.ErrorTypeRateLimit,
			},
			wantCode:  payment.FailureRateLimited,
			wantRetry: true,
		},
		{
			name: "processing errors are retryable",
			err: &stripesdk.Error{
				Code: stripesdk.ErrorCodeProcessingError,
				Type: stripesdk.ErrorTypeCard,
			},
			wantCode:  payment.FailureProcessingError,
			wantRetry: true,
		},
		{
			name:      "non Stripe errors are treated as transient provider failures",
			err:       errors.New("private transport detail"),
			wantCode:  payment.FailureProviderUnavailable,
			wantRetry: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failure := failureFromError(tt.err)
			if failure.Code != tt.wantCode || failure.Retryable != tt.wantRetry {
				t.Fatalf("failure = %+v, want code %q and retryable %t", failure, tt.wantCode, tt.wantRetry)
			}
			if failure.Cause != tt.err {
				t.Fatal("failure should retain the original error for server-side diagnostics")
			}
			if tt.wantRaw && (failure.ProviderCode == "" || failure.ProviderDeclineCode == "") {
				t.Fatalf("provider codes were not retained: %+v", failure)
			}
			if failure.Message == "" || failure.Message == "private provider detail" || failure.Message == "private transport detail" {
				t.Fatalf("client message leaked provider detail or was empty: %q", failure.Message)
			}
		})
	}
}
