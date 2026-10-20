package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const paymentIntentBody = `{
  "id": "pi_1",
  "object": "payment_intent",
  "status": "succeeded",
  "amount": 4900,
  "currency": "usd",
  "client_secret": " pi_1_secret ",
  "customer": {"id": "cus_1", "object": "customer"},
  "payment_method": {"id": "pm_1", "object": "payment_method"},
  "latest_charge": {"id": "ch_1", "object": "charge", "payment_method_details": {"card": {"brand": "visa", "last4": "4242"}}}
}`

func TestCreateOneTimeChargeBuildsRequest(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, paymentIntentBody)

	intent, appErr := provider.CreateOneTimeCharge(context.Background(), &payment.OneTimeCharge{
		Amount:             payment.NewMoney(4900, "usd"),
		Description:        "Report",
		CustomerAccountRef: "acct_1",
		IdempotencyKey:     "charge:1",
	})
	if appErr != nil {
		t.Fatalf("CreateOneTimeCharge returned an error: %v", appErr)
	}

	if captured.method != "POST" || captured.path != "/v1/payment_intents" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if captured.formValue("amount") != "4900" || captured.formValue("currency") != "usd" {
		t.Fatalf("amount/currency = %q/%q", captured.formValue("amount"), captured.formValue("currency"))
	}

	if captured.idempotencyKey != "charge:1" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}

	if !intent.Confirmed() {
		t.Error("succeeded intent must be confirmed")
	}

	if intent.Amount != payment.NewMoney(4900, "usd") {
		t.Fatalf("amount = %v", intent.Amount)
	}

	// client_secret is trimmed; card details flow off the latest charge.
	if intent.ClientSecret != "pi_1_secret" {
		t.Fatalf("client secret = %q", intent.ClientSecret)
	}

	if intent.CardBrand != "visa" || intent.CardLast4 != "4242" {
		t.Fatalf("card = %q %q", intent.CardBrand, intent.CardLast4)
	}

	if intent.CustomerRef != "cus_1" || intent.PaymentMethodRef != "pm_1" {
		t.Fatalf("refs = %q %q", intent.CustomerRef, intent.PaymentMethodRef)
	}
}

func TestFetchPaymentIntentExpandsCard(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, paymentIntentBody)

	if _, appErr := provider.FetchPaymentIntent(context.Background(), "pi_1"); appErr != nil {
		t.Fatalf("FetchPaymentIntent returned an error: %v", appErr)
	}

	if captured.method != "GET" || captured.path != "/v1/payment_intents/pi_1" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if got := captured.formValue("expand[0]"); got != "latest_charge.payment_method_details" {
		t.Fatalf("expand = %q", got)
	}
}

func TestCreateOneTimeChargeManualCapture(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, paymentIntentBody)

	_, appErr := provider.CreateOneTimeCharge(context.Background(), &payment.OneTimeCharge{
		Amount:        payment.NewMoney(4900, "usd"),
		ManualCapture: true,
	})
	if appErr != nil {
		t.Fatalf("CreateOneTimeCharge returned an error: %v", appErr)
	}

	if got := captured.formValue("capture_method"); got != "manual" {
		t.Fatalf("capture_method = %q, want manual", got)
	}
}

func TestCapturePaymentFullAndPartial(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, paymentIntentBody)

	// Full capture: no amount_to_capture is sent.
	if _, appErr := provider.CapturePayment(context.Background(), &payment.PaymentCapture{PaymentIntentRef: "pi_1"}); appErr != nil {
		t.Fatalf("CapturePayment (full) returned an error: %v", appErr)
	}

	if captured.path != "/v1/payment_intents/pi_1/capture" {
		t.Fatalf("path = %q", captured.path)
	}

	if got := captured.formValue("amount_to_capture"); got != "" {
		t.Fatalf("amount_to_capture = %q, want omitted for full capture", got)
	}

	// Partial capture: amount_to_capture is sent.
	if _, appErr := provider.CapturePayment(context.Background(), &payment.PaymentCapture{
		PaymentIntentRef: "pi_1",
		Amount:           payment.NewMoney(3000, "usd"),
	}); appErr != nil {
		t.Fatalf("CapturePayment (partial) returned an error: %v", appErr)
	}

	if got := captured.formValue("amount_to_capture"); got != "3000" {
		t.Fatalf("amount_to_capture = %q, want 3000", got)
	}
}

func TestCancelAbandonedSendsReason(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, paymentIntentBody)

	if _, appErr := provider.CancelAbandoned(context.Background(), &payment.PaymentIntentRef{PaymentIntentRef: "pi_1"}); appErr != nil {
		t.Fatalf("CancelAbandoned returned an error: %v", appErr)
	}

	if captured.path != "/v1/payment_intents/pi_1/cancel" {
		t.Fatalf("path = %q", captured.path)
	}

	if got := captured.formValue("cancellation_reason"); got != "abandoned" {
		t.Fatalf("cancellation_reason = %q", got)
	}
}

func TestPaymentIntentNilGuards(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, paymentIntentBody)

	if _, appErr := provider.CreateOneTimeCharge(context.Background(), nil); appErr == nil {
		t.Error("nil charge must be rejected")
	}

	if _, appErr := provider.CancelAbandoned(context.Background(), nil); appErr == nil {
		t.Error("nil cancel must be rejected")
	}
}
