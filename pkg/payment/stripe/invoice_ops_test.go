package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const openInvoiceListBody = `{
  "object": "list",
  "url": "/v1/invoices",
  "has_more": false,
  "data": [
    {"id": "in_1", "object": "invoice", "currency": "usd", "status": "open", "total": 5000, "amount_due": 5000}
  ]
}`

const singleInvoiceBody = `{
  "id": "in_9", "object": "invoice", "currency": "usd", "status": "paid", "total": 5000, "amount_due": 0, "amount_paid": 5000
}`

func TestListOpenInvoicesFiltersOnStatusOpen(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, openInvoiceListBody)

	invoices, appErr := provider.ListOpenInvoices(context.Background(), &payment.OpenInvoiceQuery{
		SubscriptionRef: "sub_1",
		Limit:           5,
	})
	if appErr != nil {
		t.Fatalf("ListOpenInvoices returned an error: %v", appErr)
	}

	if captured.formValue("subscription") != "sub_1" || captured.formValue("status") != "open" {
		t.Fatalf("subscription/status = %q/%q", captured.formValue("subscription"), captured.formValue("status"))
	}

	if len(invoices) != 1 || invoices[0].Total != payment.NewMoney(5000, "usd") {
		t.Fatalf("invoices = %+v", invoices)
	}
}

func TestPreviewUpgradeChargeUsesAlwaysInvoiceProration(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, singleInvoiceBody)

	_, appErr := provider.PreviewUpgradeCharge(context.Background(), &payment.PlanChangePreview{
		SubscriptionRef: "sub_1",
		ItemRef:         "si_1",
		PriceRef:        "price_2",
	})
	if appErr != nil {
		t.Fatalf("PreviewUpgradeCharge returned an error: %v", appErr)
	}

	if captured.method != "POST" || captured.path != "/v1/invoices/create_preview" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if got := captured.formValue("subscription_details[proration_behavior]"); got != "always_invoice" {
		t.Fatalf("proration_behavior = %q", got)
	}
}

func TestPayOutOfBandAndVoid(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, singleInvoiceBody)

	if _, appErr := provider.PayOutOfBand(context.Background(), &payment.InvoiceRef{InvoiceRef: "in_9", IdempotencyKey: "pay:1"}); appErr != nil {
		t.Fatalf("PayOutOfBand returned an error: %v", appErr)
	}

	if captured.path != "/v1/invoices/in_9/pay" || captured.formValue("paid_out_of_band") != "true" {
		t.Fatalf("pay: %s paid_out_of_band=%q", captured.path, captured.formValue("paid_out_of_band"))
	}

	if _, appErr := provider.Void(context.Background(), &payment.InvoiceRef{InvoiceRef: "in_9"}); appErr != nil {
		t.Fatalf("Void returned an error: %v", appErr)
	}

	if captured.path != "/v1/invoices/in_9/void" {
		t.Fatalf("void path = %q", captured.path)
	}
}

func TestInvoiceOpsNilGuards(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, singleInvoiceBody)

	if _, appErr := provider.ListOpenInvoices(context.Background(), nil); appErr == nil {
		t.Error("nil open invoice query must be rejected")
	}

	if _, appErr := provider.PayOutOfBand(context.Background(), nil); appErr == nil {
		t.Error("nil pay must be rejected")
	}

	if _, appErr := provider.Void(context.Background(), nil); appErr == nil {
		t.Error("nil void must be rejected")
	}
}
