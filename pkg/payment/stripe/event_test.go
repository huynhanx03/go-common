package stripe

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

func decoderProvider() *Provider {
	return newWithClient(stripesdk.NewClient("sk_test_dummy"), newTestLogger(), "whsec_test")
}

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

	if _, appErr := provider.VerifyAndDecode(context.Background(), []byte("{}"), ""); appErr == nil {
		t.Fatal("a missing signature header must be rejected")
	} else if appErr.Code != payment.CodePaymentInvalid {
		t.Fatalf("missing signature code = %d", appErr.Code)
	}

	noSecret := newWithClient(provider.client, newTestLogger(), "")
	if _, appErr := noSecret.VerifyAndDecode(context.Background(), []byte("{}"), "sig"); appErr == nil {
		t.Fatal("a missing webhook secret must be rejected")
	} else if appErr.Code != payment.CodePaymentProviderError {
		t.Fatalf("missing secret code = %d", appErr.Code)
	}
}

func TestDecodeWebhookResourcesMapsToNeutralModels(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		decode  func([]byte) (any, error)
		check   func(*testing.T, any)
	}{
		{
			name:    "payment intent",
			payload: `{"id":"pi_1","object":"payment_intent","status":"succeeded","amount":1299,"currency":"usd","customer":"cus_1","payment_method":"pm_1"}`,
			decode: func(payload []byte) (any, error) {
				got, appErr := decoderProvider().DecodePaymentIntent(payload)
				if appErr != nil {
					return nil, appErr
				}
				return got, nil
			},
			check: func(t *testing.T, got any) {
				intent := got.(*payment.PaymentIntent)
				if intent.Ref != "pi_1" || intent.Status != payment.PaymentIntentStatusSucceeded || intent.Amount != payment.NewMoney(1299, "usd") {
					t.Fatalf("payment intent = %+v", intent)
				}
				if intent.CustomerRef != "cus_1" || intent.PaymentMethodRef != "pm_1" {
					t.Fatalf("payment references = %+v", intent)
				}
			},
		},
		{
			name:    "setup intent",
			payload: `{"id":"seti_1","object":"setup_intent","status":"succeeded","customer":"cus_1","payment_method":"pm_1"}`,
			decode: func(payload []byte) (any, error) {
				got, appErr := decoderProvider().DecodeSetupIntent(payload)
				if appErr != nil {
					return nil, appErr
				}
				return got, nil
			},
			check: func(t *testing.T, got any) {
				intent := got.(*payment.SetupIntent)
				if intent.Ref != "seti_1" || intent.CustomerRef != "cus_1" || intent.PaymentMethodRef != "pm_1" || !intent.Succeeded() {
					t.Fatalf("setup intent = %+v", intent)
				}
			},
		},
		{
			name:    "checkout session",
			payload: `{"id":"cs_1","object":"checkout.session","status":"complete","payment_status":"paid","currency":"usd","amount_total":1500,"total_details":{"amount_tax":100},"customer":"cus_1","subscription":"sub_1","invoice":"in_1","payment_intent":"pi_1"}`,
			decode: func(payload []byte) (any, error) {
				got, appErr := decoderProvider().DecodeCheckout(payload)
				if appErr != nil {
					return nil, appErr
				}
				return got, nil
			},
			check: func(t *testing.T, got any) {
				checkout := got.(*payment.Checkout)
				if checkout.Ref != "cs_1" || !checkout.Confirmed() || checkout.CustomerRef != "cus_1" || checkout.SubscriptionRef != "sub_1" || checkout.InvoiceRef != "in_1" || checkout.PaymentIntentRef != "pi_1" {
					t.Fatalf("checkout = %+v", checkout)
				}
				if checkout.AmountTotal != payment.NewMoney(1500, "usd") || checkout.AmountTax != payment.NewMoney(100, "usd") {
					t.Fatalf("checkout amounts = total %v, tax %v", checkout.AmountTotal, checkout.AmountTax)
				}
			},
		},
		{
			name:    "subscription schedule",
			payload: `{"id":"sub_sched_1","object":"subscription_schedule","status":"active","subscription":"sub_1"}`,
			decode: func(payload []byte) (any, error) {
				got, appErr := decoderProvider().DecodeSchedule(payload)
				if appErr != nil {
					return nil, appErr
				}
				return got, nil
			},
			check: func(t *testing.T, got any) {
				schedule := got.(*payment.Schedule)
				if schedule.Ref != "sub_sched_1" || schedule.SubscriptionRef != "sub_1" || schedule.Status != payment.ScheduleStatusActive {
					t.Fatalf("schedule = %+v", schedule)
				}
			},
		},
		{
			name:    "product",
			payload: `{"id":"prod_1","object":"product","name":"Premium","active":true,"description":"Paid tier","marketing_features":[{"name":"Priority support"},{"name":""},null]}`,
			decode: func(payload []byte) (any, error) {
				got, appErr := decoderProvider().DecodeProduct(payload)
				if appErr != nil {
					return nil, appErr
				}
				return got, nil
			},
			check: func(t *testing.T, got any) {
				product := got.(*payment.Product)
				if product.Ref != "prod_1" || product.Name != "Premium" || !product.Active || len(product.MarketingFeatures) != 1 || product.MarketingFeatures[0] != "Priority support" {
					t.Fatalf("product = %+v", product)
				}
			},
		},
		{
			name:    "recurring price",
			payload: `{"id":"price_1","object":"price","active":true,"type":"recurring","unit_amount":1299,"currency":"usd","tax_behavior":"exclusive","product":"prod_1","recurring":{"interval":"month","interval_count":3,"trial_period_days":7}}`,
			decode: func(payload []byte) (any, error) {
				got, appErr := decoderProvider().DecodePrice(payload)
				if appErr != nil {
					return nil, appErr
				}
				return got, nil
			},
			check: func(t *testing.T, got any) {
				price := got.(*payment.Price)
				if price.Ref != "price_1" || price.ProductRef != "prod_1" || !price.Active || price.Type != payment.PriceTypeRecurring || price.UnitAmount != payment.NewMoney(1299, "usd") {
					t.Fatalf("price = %+v", price)
				}
				if price.Recurring == nil || price.Recurring.Interval != payment.RecurringIntervalMonth || price.Recurring.IntervalCount != 3 || price.Recurring.TrialPeriodDays != 7 {
					t.Fatalf("recurrence = %+v", price.Recurring)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.decode([]byte(tt.payload))
			if err != nil {
				t.Fatalf("decode returned error: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func TestDecodeRejectsMalformedResourcePayloads(t *testing.T) {
	provider := decoderProvider()
	decoders := []struct {
		name string
		call func([]byte) *apperr.AppError
	}{
		{name: "subscription", call: func(b []byte) *apperr.AppError { _, err := provider.DecodeSubscription(b); return err }},
		{name: "invoice", call: func(b []byte) *apperr.AppError { _, err := provider.DecodeInvoice(b); return err }},
		{name: "payment intent", call: func(b []byte) *apperr.AppError { _, err := provider.DecodePaymentIntent(b); return err }},
		{name: "setup intent", call: func(b []byte) *apperr.AppError { _, err := provider.DecodeSetupIntent(b); return err }},
		{name: "checkout", call: func(b []byte) *apperr.AppError { _, err := provider.DecodeCheckout(b); return err }},
		{name: "schedule", call: func(b []byte) *apperr.AppError { _, err := provider.DecodeSchedule(b); return err }},
		{name: "product", call: func(b []byte) *apperr.AppError { _, err := provider.DecodeProduct(b); return err }},
		{name: "price", call: func(b []byte) *apperr.AppError { _, err := provider.DecodePrice(b); return err }},
		{name: "dispute", call: func(b []byte) *apperr.AppError { _, err := provider.DecodeDispute(b); return err }},
	}

	for _, decoder := range decoders {
		t.Run(decoder.name, func(t *testing.T) {
			if appErr := decoder.call([]byte(`{"id":`)); appErr == nil || appErr.Code != payment.CodePaymentProviderError {
				t.Fatalf("malformed provider payload error = %+v", appErr)
			}
		})
	}
}

func TestVerifyAndDecodeAuthenticatesAndChecksAPITrain(t *testing.T) {
	provider := decoderProvider()
	created := time.Unix(1_700_000_000, 0).UTC()
	payload := []byte(fmt.Sprintf(`{"id":"evt_1","object":"event","api_version":%q,"created":%d,"type":"payment_intent.succeeded","data":{"object":{"id":"pi_1","object":"payment_intent"}}}`, stripesdk.APIVersion, created.Unix()))
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   payload,
		Secret:    "whsec_test",
		Timestamp: time.Now(),
	})

	event, appErr := provider.VerifyAndDecode(context.Background(), payload, signed.Header)
	if appErr != nil {
		t.Fatalf("VerifyAndDecode rejected a correctly signed event: %v", appErr)
	}
	if event.ID != "evt_1" || event.Kind != payment.EventKind("payment_intent.succeeded") || !event.OccurredAt.Equal(created) {
		t.Fatalf("event = %+v", event)
	}
	if !strings.Contains(string(event.Payload), `"id":"pi_1"`) {
		t.Fatalf("event payload was not preserved: %s", event.Payload)
	}

	badSignature := strings.Replace(signed.Header, "v1=", "v1=00", 1)
	if _, appErr = provider.VerifyAndDecode(context.Background(), payload, badSignature); appErr == nil || appErr.Code != payment.CodePaymentInvalid {
		t.Fatalf("invalid signature error = %+v", appErr)
	}

	wrongTrainPayload := []byte(fmt.Sprintf(`{"id":"evt_2","object":"event","api_version":"1900-01-01.unknown","created":%d,"type":"invoice.created","data":{"object":{"id":"in_1"}}}`, created.Unix()))
	wrongTrain := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   wrongTrainPayload,
		Secret:    "whsec_test",
		Timestamp: time.Now(),
	})
	if _, appErr = provider.VerifyAndDecode(context.Background(), wrongTrainPayload, wrongTrain.Header); appErr == nil || appErr.Code != payment.CodePaymentProviderError {
		t.Fatalf("incompatible API train error = %+v", appErr)
	}
}
