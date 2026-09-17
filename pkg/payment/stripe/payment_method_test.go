package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const paymentMethodListBody = `{
  "object": "list",
  "url": "/v1/payment_methods",
  "has_more": false,
  "data": [
    {"id": "pm_card", "object": "payment_method", "type": "card",
     "card": {"brand": "visa", "last4": "4242", "exp_month": 12, "exp_year": 2030, "fingerprint": "fp1"},
     "billing_details": {"name": "Jane Doe"}},
    {"id": "pm_bank", "object": "payment_method", "type": "us_bank_account",
     "us_bank_account": {"last4": "6789"}}
  ]
}`

const detachedCardBody = `{
  "id": "pm_card", "object": "payment_method", "type": "card",
  "card": {"brand": "visa", "last4": "4242", "exp_month": 1, "exp_year": 2031}
}`

func TestListPaymentMethodsMapsCardAndBankRail(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, paymentMethodListBody)

	methods, appErr := provider.ListPaymentMethods(context.Background(), "acct_1")
	if appErr != nil {
		t.Fatalf("ListPaymentMethods returned an error: %v", appErr)
	}

	if captured.formValue("customer_account") != "acct_1" {
		t.Fatalf("customer_account = %q", captured.formValue("customer_account"))
	}

	if len(methods) != 2 {
		t.Fatalf("methods = %+v", methods)
	}

	card := methods[0]
	if card.Card == nil || card.Card.Brand != "visa" || card.Card.Last4 != "4242" || card.Card.CardholderName != "Jane Doe" {
		t.Fatalf("card = %+v", card.Card)
	}

	bank := methods[1]
	if bank.BankDebit == nil || bank.BankDebit.Rail != payment.PaymentMethodTypeUSBankAccount || bank.BankDebit.Last4 != "6789" {
		t.Fatalf("bank = %+v", bank.BankDebit)
	}
}

func TestDetachPaymentMethod(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, detachedCardBody)

	method, appErr := provider.Detach(context.Background(), &payment.PaymentMethodDetach{
		PaymentMethodRef: "pm_card",
		IdempotencyKey:   "detach:1",
	})
	if appErr != nil {
		t.Fatalf("Detach returned an error: %v", appErr)
	}

	if captured.path != "/v1/payment_methods/pm_card/detach" {
		t.Fatalf("path = %q", captured.path)
	}

	if captured.idempotencyKey != "detach:1" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}

	if method.Ref != "pm_card" {
		t.Fatalf("ref = %q", method.Ref)
	}
}

func TestUpdatePaymentMethodSendsCardAndName(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, detachedCardBody)

	_, appErr := provider.UpdatePaymentMethod(context.Background(), &payment.PaymentMethodUpdate{
		PaymentMethodRef: "pm_card",
		ExpMonth:         1,
		ExpYear:          2031,
		CardholderName:   "Jane Roe",
	})
	if appErr != nil {
		t.Fatalf("UpdatePaymentMethod returned an error: %v", appErr)
	}

	if captured.path != "/v1/payment_methods/pm_card" {
		t.Fatalf("path = %q", captured.path)
	}

	if captured.formValue("card[exp_month]") != "1" || captured.formValue("card[exp_year]") != "2031" {
		t.Fatalf("card exp = %q/%q", captured.formValue("card[exp_month]"), captured.formValue("card[exp_year]"))
	}

	if got := captured.formValue("billing_details[name]"); got != "Jane Roe" {
		t.Fatalf("billing name = %q", got)
	}
}

func TestPaymentMethodNilGuards(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, detachedCardBody)

	if _, appErr := provider.Detach(context.Background(), nil); appErr == nil {
		t.Error("nil detach must be rejected")
	}

	if _, appErr := provider.UpdatePaymentMethod(context.Background(), nil); appErr == nil {
		t.Error("nil update must be rejected")
	}
}
