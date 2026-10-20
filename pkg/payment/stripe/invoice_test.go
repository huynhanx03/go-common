package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const taxedInvoiceBody = `{
  "id": "in_1",
  "object": "invoice",
  "currency": "usd",
  "status": "open",
  "amount_due": 11000,
  "amount_paid": 0,
  "subtotal": 10000,
  "total": 11000,
  "total_excluding_tax": 10000,
  "automatic_tax": {"enabled": true},
  "total_taxes": [
    {"amount": 1000, "taxable_amount": 10000, "tax_behavior": "exclusive", "tax_rate_details": {"tax_rate": "txr_1"}}
  ],
  "lines": {
    "object": "list",
    "data": [
      {"id": "il_1", "object": "line_item", "amount": 10000, "currency": "usd", "description": "Pro",
       "taxes": [{"amount": 1000, "taxable_amount": 10000, "tax_behavior": "exclusive", "tax_rate_details": {"tax_rate": "txr_1"}}]}
    ]
  }
}`

func TestFetchInvoiceMapsMoneyAndTax(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, taxedInvoiceBody)

	inv, appErr := provider.FetchInvoice(context.Background(), "in_1")
	if appErr != nil {
		t.Fatalf("FetchInvoice returned an error: %v", appErr)
	}

	if inv.Total != payment.NewMoney(11000, "usd") {
		t.Fatalf("Total = %v", inv.Total)
	}

	if inv.TotalExcludingTax != payment.NewMoney(10000, "usd") {
		t.Fatalf("TotalExcludingTax = %v", inv.TotalExcludingTax)
	}

	if !inv.AutomaticTax {
		t.Fatal("AutomaticTax must be true")
	}

	if inv.Tax != payment.NewMoney(1000, "usd") {
		t.Fatalf("Tax = %v, want 1000 usd", inv.Tax)
	}

	if len(inv.TaxBreakdown) != 1 {
		t.Fatalf("TaxBreakdown = %+v", inv.TaxBreakdown)
	}

	tax := inv.TaxBreakdown[0]
	if tax.Amount != payment.NewMoney(1000, "usd") || tax.TaxableAmount != payment.NewMoney(10000, "usd") {
		t.Fatalf("tax breakdown amounts = %+v", tax)
	}

	if tax.Inclusive {
		t.Error("exclusive tax must not be marked inclusive")
	}

	if tax.TaxRateRef != "txr_1" {
		t.Fatalf("TaxRateRef = %q", tax.TaxRateRef)
	}

	if len(inv.Lines) != 1 {
		t.Fatalf("lines = %+v", inv.Lines)
	}

	line := inv.Lines[0]
	if line.Amount != payment.NewMoney(10000, "usd") {
		t.Fatalf("line amount = %v", line.Amount)
	}

	if len(line.TaxAmounts) != 1 || line.TaxAmounts[0].Amount != payment.NewMoney(1000, "usd") {
		t.Fatalf("line tax amounts = %+v", line.TaxAmounts)
	}
}
