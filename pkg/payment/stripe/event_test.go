package stripe

import (
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

func TestDecodeSubscriptionReadsPlanRefsFromPayload(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, "{}")

	// plan.product as a bare string ID.
	payload := []byte(`{
	  "id": "sub_1", "object": "subscription", "status": "active",
	  "plan": {"id": "price_1", "product": "prod_1"},
	  "items": {"data": [{"id": "si_1", "price": {"id": "price_1", "unit_amount": 1000, "currency": "usd"}}]}
	}`)

	sub, appErr := provider.DecodeSubscription(payload)
	if appErr != nil {
		t.Fatalf("DecodeSubscription returned an error: %v", appErr)
	}

	if sub.PlanPriceRef != "price_1" {
		t.Fatalf("PlanPriceRef = %q", sub.PlanPriceRef)
	}

	if sub.PlanProductRef != "prod_1" {
		t.Fatalf("PlanProductRef = %q", sub.PlanProductRef)
	}

	if len(sub.Items) != 1 || sub.Items[0].UnitAmount != payment.NewMoney(1000, "usd") {
		t.Fatalf("items = %+v", sub.Items)
	}
}

func TestDecodeSubscriptionReadsExpandedProduct(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, "{}")

	// plan.product as an expanded object.
	payload := []byte(`{
	  "id": "sub_1", "object": "subscription", "status": "active",
	  "plan": {"id": "price_9", "product": {"id": "prod_9", "object": "product"}}
	}`)

	sub, appErr := provider.DecodeSubscription(payload)
	if appErr != nil {
		t.Fatalf("DecodeSubscription returned an error: %v", appErr)
	}

	if sub.PlanProductRef != "prod_9" {
		t.Fatalf("PlanProductRef = %q, want prod_9", sub.PlanProductRef)
	}
}

func TestDecodeInvoiceBackfillsLinePriceRefs(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, "{}")

	payload := []byte(`{
	  "id": "in_1", "object": "invoice", "currency": "usd", "status": "open",
	  "subscription": "sub_7",
	  "lines": {"object": "list", "data": [{"id": "il_1", "amount": 500, "currency": "usd", "price": {"id": "price_7"}}]}
	}`)

	inv, appErr := provider.DecodeInvoice(payload)
	if appErr != nil {
		t.Fatalf("DecodeInvoice returned an error: %v", appErr)
	}

	if inv.SubscriptionRef != "sub_7" {
		t.Fatalf("SubscriptionRef = %q", inv.SubscriptionRef)
	}

	if len(inv.Lines) != 1 || inv.Lines[0].PriceRef != "price_7" {
		t.Fatalf("lines = %+v", inv.Lines)
	}
}

func TestDecodeDispute(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, "{}")

	payload := []byte(`{
	  "id": "dp_1", "object": "dispute", "status": "needs_response", "reason": "fraudulent",
	  "amount": 4900, "currency": "usd",
	  "charge": {"id": "ch_1", "object": "charge"},
	  "payment_intent": {"id": "pi_1", "object": "payment_intent"}
	}`)

	dispute, appErr := provider.DecodeDispute(payload)
	if appErr != nil {
		t.Fatalf("DecodeDispute returned an error: %v", appErr)
	}

	if dispute.Ref != "dp_1" || dispute.ChargeRef != "ch_1" || dispute.PaymentIntentRef != "pi_1" {
		t.Fatalf("dispute refs = %+v", dispute)
	}

	if dispute.Status != payment.DisputeStatusNeedsResponse || dispute.Reason != payment.DisputeReasonFraudulent {
		t.Fatalf("status/reason = %q / %q", dispute.Status, dispute.Reason)
	}

	if dispute.Amount != payment.NewMoney(4900, "usd") {
		t.Fatalf("amount = %v", dispute.Amount)
	}
}

func TestVerifyAndDecodeRejectsMissingSignatureAndSecret(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, "{}")

	if _, appErr := provider.VerifyAndDecode([]byte("{}"), ""); appErr == nil {
		t.Fatal("a missing signature header must be rejected")
	} else if appErr.Code != payment.CodePaymentInvalid {
		t.Fatalf("missing signature code = %d", appErr.Code)
	}

	noSecret := newWithClient(provider.client, nil, "")
	if _, appErr := noSecret.VerifyAndDecode([]byte("{}"), "sig"); appErr == nil {
		t.Fatal("a missing webhook secret must be rejected")
	} else if appErr.Code != payment.CodePaymentProviderError {
		t.Fatalf("missing secret code = %d", appErr.Code)
	}
}
