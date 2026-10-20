package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const setupIntentBody = `{
  "id": "seti_1",
  "object": "setup_intent",
  "status": "succeeded",
  "client_secret": "seti_1_secret",
  "customer": {"id": "cus_1", "object": "customer"},
  "payment_method": {"id": "pm_1", "object": "payment_method"},
  "metadata": {"purpose": "save-card"}
}`

func TestCreateSetupIntentBuildsOffSessionRequest(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, setupIntentBody)

	setup, appErr := provider.CreateSetupIntent(context.Background(), &payment.NewSetupIntent{
		CustomerAccountRef: "acct_1",
		Metadata:           map[string]string{"purpose": "save-card"},
		IdempotencyKey:     "setup:1",
	})
	if appErr != nil {
		t.Fatalf("CreateSetupIntent returned an error: %v", appErr)
	}

	if captured.method != "POST" || captured.path != "/v1/setup_intents" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if got := captured.formValue("usage"); got != "off_session" {
		t.Fatalf("usage = %q", got)
	}

	if got := captured.formValue("payment_method_types[0]"); got != "card" {
		t.Fatalf("payment_method_types[0] = %q", got)
	}

	if captured.idempotencyKey != "setup:1" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}

	if !setup.Succeeded() {
		t.Error("succeeded setup intent")
	}

	if setup.CustomerRef != "cus_1" || setup.PaymentMethodRef != "pm_1" {
		t.Fatalf("refs = %q %q", setup.CustomerRef, setup.PaymentMethodRef)
	}
}

func TestCreateSetupIntentRejectsNil(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, setupIntentBody)

	if _, appErr := provider.CreateSetupIntent(context.Background(), nil); appErr == nil {
		t.Error("nil setup intent must be rejected")
	}
}
