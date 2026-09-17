package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/payment"
)

const v2AccountBody = `{
  "id": "acct_1",
  "object": "v2.core.account",
  "configuration": {"customer": {"billing": {"default_payment_method": "pm_1"}}}
}`

func TestCreateCustomerAccountRequestsTaxCapability(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, v2AccountBody)

	account, appErr := provider.CreateCustomerAccount(context.Background(), &payment.NewCustomerAccount{
		ContactEmail:   "vn@example.test",
		DisplayName:    "VN Customer",
		IdempotencyKey: "acct:1",
	})
	if appErr != nil {
		t.Fatalf("CreateCustomerAccount returned an error: %v", appErr)
	}

	if captured.method != "POST" || captured.path != "/v2/core/accounts" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	// The automatic indirect tax capability must be requested so tax can be
	// computed once a location is known (collected during Checkout).
	if requested, _ := json.GetBool(captured.body, "configuration.customer.capabilities.automatic_indirect_tax.requested"); !requested {
		t.Fatalf("automatic_indirect_tax not requested, body = %s", captured.body)
	}

	// No customer address is sent: the V2 AddressParams type would serialize
	// with unrecognized field names, so the driver omits it deliberately.
	if json.Exists(captured.body, "configuration.customer.shipping") {
		t.Fatalf("no shipping address should be sent, body = %s", captured.body)
	}

	if captured.idempotencyKey != "acct:1" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}

	if account.Ref != "acct_1" {
		t.Fatalf("ref = %q", account.Ref)
	}
}

func TestFetchCustomerAccountResolvesDefaultPaymentMethod(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, v2AccountBody)

	account, appErr := provider.FetchCustomerAccount(context.Background(), "acct_1")
	if appErr != nil {
		t.Fatalf("FetchCustomerAccount returned an error: %v", appErr)
	}

	if captured.method != "GET" || captured.path != "/v2/core/accounts/acct_1" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if account.DefaultPaymentMethodRef != "pm_1" {
		t.Fatalf("default pm = %q", account.DefaultPaymentMethodRef)
	}
}

func TestSetAccountDefaultPaymentMethod(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, v2AccountBody)

	_, appErr := provider.SetAccountDefaultPaymentMethod(context.Background(), &payment.CustomerDefaultPaymentMethodChange{
		CustomerAccountRef: "acct_1",
		PaymentMethodRef:   "pm_2",
	})
	if appErr != nil {
		t.Fatalf("SetAccountDefaultPaymentMethod returned an error: %v", appErr)
	}

	if captured.path != "/v2/core/accounts/acct_1" {
		t.Fatalf("path = %q", captured.path)
	}

	if pm, _ := json.GetString(captured.body, "configuration.customer.billing.default_payment_method"); pm != "pm_2" {
		t.Fatalf("default pm in body = %q, body = %s", pm, captured.body)
	}
}

func TestCustomerAccountNilGuards(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, v2AccountBody)

	if _, appErr := provider.CreateCustomerAccount(context.Background(), nil); appErr == nil {
		t.Error("nil create must be rejected")
	}

	if _, appErr := provider.SetAccountDefaultPaymentMethod(context.Background(), nil); appErr == nil {
		t.Error("nil set default must be rejected")
	}
}
