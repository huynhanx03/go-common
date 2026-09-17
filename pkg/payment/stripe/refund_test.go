package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const refundBody = `{
  "id": "re_1",
  "object": "refund",
  "status": "succeeded",
  "amount": 4900,
  "currency": "usd",
  "payment_intent": {"id": "pi_1", "object": "payment_intent"},
  "charge": {"id": "ch_1", "object": "charge"}
}`

func TestRefundInFullSendsNoAmount(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, refundBody)

	refund, appErr := provider.RefundInFull(context.Background(), &payment.FullRefund{
		PaymentIntentRef: "pi_1",
		IdempotencyKey:   "refund:pi_1",
	})
	if appErr != nil {
		t.Fatalf("RefundInFull returned an error: %v", appErr)
	}

	if captured.method != "POST" || captured.path != "/v1/refunds" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if got := captured.formValue("payment_intent"); got != "pi_1" {
		t.Fatalf("payment_intent = %q", got)
	}

	// An absent amount is what makes the provider refund the whole charge.
	if got := captured.formValue("amount"); got != "" {
		t.Fatalf("amount = %q, want it omitted", got)
	}

	if got := captured.formValue("reason"); got != "" {
		t.Fatalf("reason = %q, want it omitted when unset", got)
	}

	if captured.idempotencyKey != "refund:pi_1" {
		t.Fatalf("Idempotency-Key = %q", captured.idempotencyKey)
	}

	if refund.Ref != "re_1" || refund.PaymentIntentRef != "pi_1" {
		t.Fatalf("refund = %+v", refund)
	}

	if refund.Status != payment.RefundStatusSucceeded {
		t.Fatalf("status = %q", refund.Status)
	}

	if refund.Amount != payment.NewMoney(4900, "usd") {
		t.Fatalf("amount = %v", refund.Amount)
	}
}

func TestRefundInFullSendsTheIntentReason(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, refundBody)

	_, appErr := provider.RefundInFull(context.Background(), &payment.FullRefund{
		PaymentIntentRef: "pi_1",
		Reason:           payment.RefundReasonDuplicate,
	})
	if appErr != nil {
		t.Fatalf("RefundInFull returned an error: %v", appErr)
	}

	if got := captured.formValue("reason"); got != "duplicate" {
		t.Fatalf("reason = %q, want duplicate", got)
	}
}

func TestRefundAmountSendsAmount(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, refundBody)

	refund, appErr := provider.RefundAmount(context.Background(), &payment.PartialRefund{
		PaymentIntentRef: "pi_1",
		Amount:           payment.NewMoney(2000, "usd"),
		Reason:           payment.RefundReasonRequestedByCustomer,
		IdempotencyKey:   "refund:partial:1",
	})
	if appErr != nil {
		t.Fatalf("RefundAmount returned an error: %v", appErr)
	}

	if got := captured.formValue("amount"); got != "2000" {
		t.Fatalf("amount = %q, want 2000", got)
	}

	if got := captured.formValue("reason"); got != "requested_by_customer" {
		t.Fatalf("reason = %q", got)
	}

	if refund.Ref != "re_1" {
		t.Fatalf("ref = %q", refund.Ref)
	}
}

func TestRefundAmountRejectsNonPositiveAmount(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, refundBody)

	_, appErr := provider.RefundAmount(context.Background(), &payment.PartialRefund{
		PaymentIntentRef: "pi_1",
		Amount:           payment.NewMoney(0, "usd"),
	})
	if appErr == nil {
		t.Fatal("a non-positive partial refund amount must be rejected")
	}

	if appErr.Code != payment.CodePaymentInvalid {
		t.Fatalf("code = %d", appErr.Code)
	}
}

func TestRefundInFullRejectsNilRequest(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, refundBody)

	_, appErr := provider.RefundInFull(context.Background(), nil)
	if appErr == nil {
		t.Fatal("a nil request must be rejected")
	}

	if appErr.Code != payment.CodePaymentInvalid {
		t.Fatalf("code = %d, want %d", appErr.Code, payment.CodePaymentInvalid)
	}
}

func TestUnknownRefundStatusSurvivesVerbatim(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, `{"id":"re_1","object":"refund","status":"held_for_review","currency":"usd"}`)

	refund, appErr := provider.RefundInFull(context.Background(), &payment.FullRefund{PaymentIntentRef: "pi_1"})
	if appErr != nil {
		t.Fatalf("RefundInFull returned an error: %v", appErr)
	}

	if refund.Status != payment.RefundStatus("held_for_review") {
		t.Fatalf("status = %q, want the provider value verbatim", refund.Status)
	}
}
