package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

// A VND (zero-decimal) checkout session with automatic tax applied.
const checkoutBody = `{
  "id": "cs_1",
  "object": "checkout.session",
  "status": "open",
  "payment_status": "unpaid",
  "currency": "vnd",
  "amount_total": 200000,
  "automatic_tax": {"enabled": true},
  "total_details": {"amount_tax": 20000}
}`

func TestOpenOneTimePaymentBuildsRegionalRequest(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, checkoutBody)

	checkout, appErr := provider.OpenOneTimePayment(context.Background(), &payment.OneTimeCheckout{
		CustomerAccountRef: "acct_1",
		LineItems: []payment.OneTimeLineItem{
			{Amount: payment.NewMoney(200000, "vnd"), ProductName: "One-off"},
		},
		ReturnURL:      "https://example.test/return",
		AutomaticTax:   true,
		IdempotencyKey: "checkout:1",
	})
	if appErr != nil {
		t.Fatalf("OpenOneTimePayment returned an error: %v", appErr)
	}

	if captured.method != "POST" || captured.path != "/v1/checkout/sessions" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if got := captured.formValue("mode"); got != "payment" {
		t.Fatalf("mode = %q", got)
	}

	if got := captured.formValue("automatic_tax[enabled]"); got != "true" {
		t.Fatalf("automatic_tax[enabled] = %q, want true", got)
	}

	if got := captured.formValue("line_items[0][price_data][currency]"); got != "vnd" {
		t.Fatalf("currency = %q", got)
	}

	if got := captured.formValue("line_items[0][price_data][unit_amount]"); got != "200000" {
		t.Fatalf("unit_amount = %q, want 200000 (zero-decimal VND)", got)
	}

	if captured.idempotencyKey != "checkout:1" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}

	// Response mapping: money and tax are currency-aware.
	if checkout.AmountTotal != payment.NewMoney(200000, "vnd") {
		t.Fatalf("AmountTotal = %v", checkout.AmountTotal)
	}

	if checkout.AmountTax != payment.NewMoney(20000, "vnd") {
		t.Fatalf("AmountTax = %v", checkout.AmountTax)
	}
}

func TestOpenOneTimePaymentOmitsAutomaticTaxWhenDisabled(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, checkoutBody)

	_, appErr := provider.OpenOneTimePayment(context.Background(), &payment.OneTimeCheckout{
		CustomerAccountRef: "acct_1",
		LineItems: []payment.OneTimeLineItem{
			{Amount: payment.NewMoney(1000, "usd"), ProductName: "One-off"},
		},
		ReturnURL: "https://example.test/return",
	})
	if appErr != nil {
		t.Fatalf("OpenOneTimePayment returned an error: %v", appErr)
	}

	if got := captured.formValue("automatic_tax[enabled]"); got != "" {
		t.Fatalf("automatic_tax[enabled] = %q, want it omitted", got)
	}
}

func TestOpenSubscriptionMultiItemWithPromotionCodes(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, checkoutBody)

	_, appErr := provider.OpenSubscription(context.Background(), &payment.SubscriptionCheckout{
		CustomerAccountRef: "acct_1",
		LineItems: []payment.CheckoutLineItem{
			{PriceRef: "price_1", Quantity: 2},
			{PriceRef: "price_2"}, // quantity defaults to 1
		},
		ReturnURL:           "https://example.test/return",
		AllowPromotionCodes: true,
	})
	if appErr != nil {
		t.Fatalf("OpenSubscription returned an error: %v", appErr)
	}

	if got := captured.formValue("line_items[0][price]"); got != "price_1" {
		t.Fatalf("line 0 price = %q", got)
	}

	if got := captured.formValue("line_items[0][quantity]"); got != "2" {
		t.Fatalf("line 0 quantity = %q", got)
	}

	if got := captured.formValue("line_items[1][price]"); got != "price_2" {
		t.Fatalf("line 1 price = %q", got)
	}

	if got := captured.formValue("line_items[1][quantity]"); got != "1" {
		t.Fatalf("line 1 quantity = %q (want default 1)", got)
	}

	if got := captured.formValue("allow_promotion_codes"); got != "true" {
		t.Fatalf("allow_promotion_codes = %q", got)
	}
}

func TestOpenSubscriptionDiscountWinsOverPromotionCodes(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, checkoutBody)

	_, appErr := provider.OpenSubscription(context.Background(), &payment.SubscriptionCheckout{
		CustomerAccountRef:  "acct_1",
		LineItems:           []payment.CheckoutLineItem{{PriceRef: "price_1"}},
		ReturnURL:           "https://example.test/return",
		AllowPromotionCodes: true, // ignored because explicit discounts are set
		Discounts:           []payment.Discount{{CouponRef: "coupon_10off"}},
	})
	if appErr != nil {
		t.Fatalf("OpenSubscription returned an error: %v", appErr)
	}

	if got := captured.formValue("discounts[0][coupon]"); got != "coupon_10off" {
		t.Fatalf("discounts[0][coupon] = %q", got)
	}

	// Mutually exclusive: an explicit discount suppresses allow_promotion_codes.
	if got := captured.formValue("allow_promotion_codes"); got != "" {
		t.Fatalf("allow_promotion_codes = %q, want it omitted when discounts are set", got)
	}
}
