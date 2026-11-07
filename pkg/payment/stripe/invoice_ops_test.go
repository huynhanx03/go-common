package stripe

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestFetchInvoiceWithConfirmationSecretExpandsPaymentState(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, `{
	  "id":"in_ready","object":"invoice","currency":"usd","status":"open","amount_due":5000,"total":5000,
	  "confirmation_secret":{"client_secret":"pi_secret","type":"payment_intent"},
	  "payments":{"object":"list","data":[{"id":"inpay_1","object":"invoice_payment","payment":{"type":"payment_intent","payment_intent":{"id":"pi_1","object":"payment_intent"}}}]}
	}`)

	invoice, appErr := provider.FetchInvoiceWithConfirmationSecret(context.Background(), "in_ready")
	if appErr != nil {
		t.Fatalf("FetchInvoiceWithConfirmationSecret returned an error: %v", appErr)
	}
	if captured.method != "GET" || captured.path != "/v1/invoices/in_ready" {
		t.Fatalf("request = %s %s", captured.method, captured.path)
	}
	if invoice.ConfirmationSecret != "pi_secret" || invoice.PaymentIntentRef != "pi_1" {
		t.Fatalf("invoice confirmation/payment refs = %+v", invoice)
	}
	assertQueryContains(t, captured.form, "expand", "confirmation_secret")
	assertQueryContains(t, captured.form, "expand", "payments.data.payment")
}

func TestPreviewPlanChangeLinesRequestsProrationAndExpandedLines(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, singleInvoiceBody)
	prorationDate := time.Unix(1_710_000_000, 0).UTC()
	_, appErr := provider.PreviewPlanChangeLines(context.Background(), &payment.PlanChangePreview{
		SubscriptionRef:    "sub_1",
		ItemRef:            "si_1",
		PriceRef:           "price_2",
		CustomerAccountRef: "acct_1",
		ProrationDate:      &prorationDate,
	})
	if appErr != nil {
		t.Fatalf("PreviewPlanChangeLines returned an error: %v", appErr)
	}
	if captured.method != "POST" || captured.path != "/v1/invoices/create_preview" || captured.formValue("subscription") != "sub_1" || captured.formValue("customer_account") != "acct_1" {
		t.Fatalf("preview request = %s %s with form %v", captured.method, captured.path, captured.form)
	}
	if captured.formValue("subscription_details[items][0][id]") != "si_1" || captured.formValue("subscription_details[items][0][price]") != "price_2" {
		t.Fatalf("preview line item = %v", captured.form)
	}
	if captured.formValue("subscription_details[proration_date]") != "1710000000" || captured.formValue("subscription_details[proration_behavior]") != "always_invoice" {
		t.Fatalf("proration settings = %v", captured.form)
	}
	assertQueryContains(t, captured.form, "expand", "lines.data.parent.subscription_item_details")
	assertQueryContains(t, captured.form, "expand", "lines.data.parent.invoice_item_details")
}

func TestPreviewUpcomingInvoiceUsesSubscriptionAndOptionalAccount(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, singleInvoiceBody)
	_, appErr := provider.PreviewUpcomingInvoice(context.Background(), &payment.UpcomingInvoicePreview{
		SubscriptionRef:    "sub_next",
		CustomerAccountRef: "acct_next",
	})
	if appErr != nil {
		t.Fatalf("PreviewUpcomingInvoice returned an error: %v", appErr)
	}
	if captured.method != "POST" || captured.path != "/v1/invoices/create_preview" || captured.formValue("subscription") != "sub_next" || captured.formValue("customer_account") != "acct_next" {
		t.Fatalf("upcoming invoice request = %s %s with form %v", captured.method, captured.path, captured.form)
	}
	assertQueryContains(t, captured.form, "expand", "lines.data.parent.subscription_item_details")
}

func assertQueryContains(t *testing.T, values map[string][]string, key, want string) {
	t.Helper()
	for formKey, formValues := range values {
		if formKey != key && !strings.HasPrefix(formKey, key+"[") {
			continue
		}
		for _, value := range formValues {
			if value == want {
				return
			}
		}
	}
	t.Fatalf("form parameters = %v; want %q under %q", values, want, key)
}
