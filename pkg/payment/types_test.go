package payment

import "testing"

func TestParseProrationModeRoundTrips(t *testing.T) {
	t.Parallel()

	for _, m := range []ProrationMode{ProrationNone, ProrationAlwaysInvoice} {
		parsed, ok := ParseProrationMode(m.String())
		if !ok || parsed != m {
			t.Fatalf("%q round-tripped to %v (ok=%t)", m.String(), parsed, ok)
		}
	}

	if _, ok := ParseProrationMode("nonsense"); ok {
		t.Fatal("unknown proration mode must not parse")
	}

	if ProrationMode(0).String() != "" {
		t.Fatal("undefined proration mode must stringify to empty")
	}
}

func TestParseRefundReasonRoundTrips(t *testing.T) {
	t.Parallel()

	for _, r := range []RefundReason{RefundReasonRequestedByCustomer, RefundReasonDuplicate, RefundReasonFraudulent} {
		parsed, ok := ParseRefundReason(r.String())
		if !ok || parsed != r {
			t.Fatalf("%q round-tripped to %v (ok=%t)", r.String(), parsed, ok)
		}
	}

	if _, ok := ParseRefundReason("because"); ok {
		t.Fatal("unknown refund reason must not parse")
	}
}

func TestPriceAmountForResolvesRegionalPricing(t *testing.T) {
	t.Parallel()

	price := &Price{
		UnitAmount: NewMoney(1000, "usd"),
		CurrencyOptions: map[string]Money{
			"vnd": NewMoney(250000, "vnd"),
		},
	}

	if got, ok := price.AmountFor("usd"); !ok || got != NewMoney(1000, "usd") {
		t.Fatalf("AmountFor(usd) = %v, %t", got, ok)
	}

	if got, ok := price.AmountFor("VND"); !ok || got != NewMoney(250000, "vnd") {
		t.Fatalf("AmountFor(VND) = %v, %t", got, ok)
	}

	if _, ok := price.AmountFor("eur"); ok {
		t.Fatal("a currency the price is not offered in must report ok=false")
	}

	if _, ok := (*Price)(nil).AmountFor("usd"); ok {
		t.Fatal("nil price must report ok=false")
	}
}

func TestInvoiceChargedPriceRefAndProrationTotals(t *testing.T) {
	t.Parallel()

	inv := &Invoice{Lines: []InvoiceLine{
		{PriceRef: "price_credit", Amount: NewMoney(-500, "usd"), Proration: true},
		{PriceRef: "price_charge", Amount: NewMoney(1500, "usd"), Proration: true},
	}}

	if got := inv.ChargedPriceRef(); got != "price_charge" {
		t.Fatalf("ChargedPriceRef = %q", got)
	}

	credit, charge := inv.ProrationTotals()
	if credit != 500 || charge != 1500 {
		t.Fatalf("ProrationTotals = %d, %d", credit, charge)
	}
}

func TestStatusHelpers(t *testing.T) {
	t.Parallel()

	if !(&PaymentIntent{Status: PaymentIntentStatusSucceeded}).Confirmed() {
		t.Error("succeeded intent must be confirmed")
	}

	if (&PaymentIntent{Status: PaymentIntentStatusRequiresPaymentMethod}).Confirmed() {
		t.Error("requires_payment_method must not be confirmed")
	}

	if !(&SetupIntent{Status: SetupIntentStatusSucceeded}).Succeeded() {
		t.Error("succeeded setup intent")
	}

	if !(&Checkout{Status: CheckoutStatusComplete}).Confirmed() {
		t.Error("complete checkout must be confirmed")
	}

	if _, ok := (&Subscription{}).FirstItem(); ok {
		t.Error("empty subscription has no first item")
	}
}
