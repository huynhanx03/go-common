package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const subscriptionBody = `{
  "id": "sub_1",
  "object": "subscription",
  "status": "active",
  "cancel_at_period_end": false,
  "customer": {"id": "cus_1", "object": "customer"},
  "customer_account": "acct_1",
  "schedule": {"id": "sub_sched_1", "object": "subscription_schedule"},
  "default_payment_method": {"id": "pm_1", "object": "payment_method"},
  "items": {"object": "list", "data": [
    {"id": "si_1", "object": "subscription_item",
     "price": {"id": "price_1", "object": "price", "unit_amount": 9900, "currency": "usd", "product": {"id": "prod_1", "object": "product"}}}
  ]},
  "metadata": {"plan": "pro"}
}`

func TestFetchSubscriptionMapsItemsAndRefs(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, subscriptionBody)

	sub, appErr := provider.Fetch(context.Background(), "sub_1")
	if appErr != nil {
		t.Fatalf("Fetch returned an error: %v", appErr)
	}

	if captured.method != "GET" || captured.path != "/v1/subscriptions/sub_1" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if sub.Status != payment.SubscriptionStatusActive {
		t.Fatalf("status = %q", sub.Status)
	}

	if sub.CustomerRef != "cus_1" || sub.CustomerAccountRef != "acct_1" || sub.ScheduleRef != "sub_sched_1" {
		t.Fatalf("refs = %+v", sub)
	}

	if sub.DefaultPaymentMethodRef != "pm_1" {
		t.Fatalf("default pm = %q", sub.DefaultPaymentMethodRef)
	}

	item, ok := sub.FirstItem()
	if !ok {
		t.Fatal("expected a first item")
	}

	if item.PriceRef != "price_1" || item.ProductRef != "prod_1" {
		t.Fatalf("item refs = %+v", item)
	}

	if item.UnitAmount != payment.NewMoney(9900, "usd") {
		t.Fatalf("item unit amount = %v", item.UnitAmount)
	}
}

func TestFetchWithLatestInvoiceExpandsCurrentBillingState(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, `{
	  "id":"sub_1","object":"subscription","status":"active",
	  "latest_invoice":{"id":"in_1","object":"invoice","status":"paid","currency":"usd","amount_paid":9900,"total":9900}
	}`)
	subscription, appErr := provider.FetchWithLatestInvoice(context.Background(), "sub_1")
	if appErr != nil {
		t.Fatalf("FetchWithLatestInvoice returned an error: %v", appErr)
	}
	if captured.path != "/v1/subscriptions/sub_1" {
		t.Fatalf("request path = %q", captured.path)
	}
	assertQueryContains(t, captured.form, "expand", "latest_invoice")
	if subscription.LatestInvoiceRef != "in_1" || !subscription.LatestInvoicePaid || subscription.LatestInvoice == nil || subscription.LatestInvoice.AmountPaid != payment.NewMoney(9900, "usd") {
		t.Fatalf("latest invoice mapping = %+v", subscription)
	}
}

func TestChangePlanItemSendsProrationAndItem(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, subscriptionBody)

	_, appErr := provider.ChangePlanItem(context.Background(), &payment.PlanItemChange{
		SubscriptionRef: "sub_1",
		ItemRef:         "si_1",
		PriceRef:        "price_2",
		Proration:       payment.ProrationAlwaysInvoice,
		IdempotencyKey:  "change:1",
	})
	if appErr != nil {
		t.Fatalf("ChangePlanItem returned an error: %v", appErr)
	}

	if captured.method != "POST" || captured.path != "/v1/subscriptions/sub_1" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if got := captured.formValue("items[0][id]"); got != "si_1" {
		t.Fatalf("items[0][id] = %q", got)
	}

	if got := captured.formValue("items[0][price]"); got != "price_2" {
		t.Fatalf("items[0][price] = %q", got)
	}

	if got := captured.formValue("proration_behavior"); got != "always_invoice" {
		t.Fatalf("proration_behavior = %q", got)
	}

	if captured.idempotencyKey != "change:1" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}
}

func TestUpgradeWithPendingInvoiceUsesConfirmationExpansion(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, subscriptionBody)
	_, appErr := provider.UpgradeWithPendingInvoice(context.Background(), &payment.PlanItemChange{
		SubscriptionRef: "sub_1",
		ItemRef:         "si_1",
		PriceRef:        "price_2",
		IdempotencyKey:  "upgrade:1",
	})
	if appErr != nil {
		t.Fatalf("UpgradeWithPendingInvoice returned an error: %v", appErr)
	}
	if captured.method != "POST" || captured.path != "/v1/subscriptions/sub_1" || captured.idempotencyKey != "upgrade:1" {
		t.Fatalf("upgrade request = %s %s, idempotency %q", captured.method, captured.path, captured.idempotencyKey)
	}
	if captured.formValue("payment_behavior") != "pending_if_incomplete" || captured.formValue("proration_behavior") != "always_invoice" {
		t.Fatalf("upgrade behavior = %v", captured.form)
	}
	assertQueryContains(t, captured.form, "expand", "latest_invoice.confirmation_secret")
	assertQueryContains(t, captured.form, "expand", "latest_invoice.payments.data.payment")
}

func TestResetBillingAnchorAndSetDefaultPaymentMethod(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, subscriptionBody)
	if _, appErr := provider.ResetBillingAnchorNow(context.Background(), &payment.SubscriptionRef{SubscriptionRef: "sub_1", IdempotencyKey: "anchor:1"}); appErr != nil {
		t.Fatalf("ResetBillingAnchorNow returned an error: %v", appErr)
	}
	if captured.formValue("billing_cycle_anchor") != "now" || captured.formValue("proration_behavior") != payment.ProrationNone.String() || captured.idempotencyKey != "anchor:1" {
		t.Fatalf("billing anchor request = %v, idempotency %q", captured.form, captured.idempotencyKey)
	}

	if _, appErr := provider.SetDefaultPaymentMethod(context.Background(), &payment.DefaultPaymentMethodChange{
		SubscriptionRef:  "sub_1",
		PaymentMethodRef: "pm_2",
		IdempotencyKey:   "default-pm:1",
	}); appErr != nil {
		t.Fatalf("SetDefaultPaymentMethod returned an error: %v", appErr)
	}
	if captured.formValue("default_payment_method") != "pm_2" || captured.idempotencyKey != "default-pm:1" {
		t.Fatalf("default payment method request = %v, idempotency %q", captured.form, captured.idempotencyKey)
	}
}

func TestCancelAtPeriodEndAndResumeToggleFlag(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, subscriptionBody)

	if _, appErr := provider.CancelAtPeriodEnd(context.Background(), &payment.SubscriptionRef{SubscriptionRef: "sub_1"}); appErr != nil {
		t.Fatalf("CancelAtPeriodEnd returned an error: %v", appErr)
	}

	if got := captured.formValue("cancel_at_period_end"); got != "true" {
		t.Fatalf("cancel_at_period_end = %q, want true", got)
	}

	if _, appErr := provider.Resume(context.Background(), &payment.SubscriptionRef{SubscriptionRef: "sub_1"}); appErr != nil {
		t.Fatalf("Resume returned an error: %v", appErr)
	}

	if got := captured.formValue("cancel_at_period_end"); got != "false" {
		t.Fatalf("cancel_at_period_end = %q, want false", got)
	}
}

func TestCancelNowHitsCancelEndpoint(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, subscriptionBody)

	if _, appErr := provider.CancelNow(context.Background(), &payment.SubscriptionRef{SubscriptionRef: "sub_1"}); appErr != nil {
		t.Fatalf("CancelNow returned an error: %v", appErr)
	}

	if captured.method != "DELETE" || captured.path != "/v1/subscriptions/sub_1" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}
}

func TestSubscriptionNilGuards(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, subscriptionBody)

	if _, appErr := provider.ChangePlanItem(context.Background(), nil); appErr == nil {
		t.Error("nil plan item change must be rejected")
	}

	if _, appErr := provider.SetDefaultPaymentMethod(context.Background(), nil); appErr == nil {
		t.Error("nil default payment method change must be rejected")
	}
}
