package stripe

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

const (
	paymentBehaviorDefaultIncomplete   = "default_incomplete"
	paymentBehaviorPendingIfIncomplete = "pending_if_incomplete"
)

// Fetch retrieves a subscription.
func (p *Provider) Fetch(ctx context.Context, ref string) (*payment.Subscription, *apperr.AppError) {
	return p.retrieveSubscription(ctx, ref, nil)
}

// FetchWithLatestInvoice retrieves a subscription with its latest invoice
// expanded, so the caller sees the current billing state in one round trip.
func (p *Provider) FetchWithLatestInvoice(ctx context.Context, ref string) (*payment.Subscription, *apperr.AppError) {
	return p.retrieveSubscription(ctx, ref, &stripesdk.SubscriptionRetrieveParams{
		Expand: []*string{stripesdk.String("latest_invoice")},
	})
}

// ChangePlanItem re-prices one subscription item.
func (p *Provider) ChangePlanItem(ctx context.Context, in *payment.PlanItemChange) (*payment.Subscription, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil plan item change request")
	}

	params := &stripesdk.SubscriptionUpdateParams{
		Items: []*stripesdk.SubscriptionUpdateItemParams{
			{
				ID:    stripesdk.String(in.ItemRef),
				Price: stripesdk.String(in.PriceRef),
			},
		},
		ProrationBehavior: stripesdk.String(in.Proration.String()),
	}

	if in.ProrationDate != nil && !in.ProrationDate.IsZero() {
		params.ProrationDate = stripesdk.Int64(in.ProrationDate.Unix())
	}

	if in.DeferPayment {
		params.PaymentBehavior = stripesdk.String(paymentBehaviorDefaultIncomplete)
	}

	params.AddExpand("latest_invoice")
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.updateSubscription(ctx, in.SubscriptionRef, params, "stripe subscription plan item change failed")
}

// UpgradeWithPendingInvoice re-prices an item and immediately invoices the
// proration, leaving the invoice payable so the caller can confirm it.
func (p *Provider) UpgradeWithPendingInvoice(ctx context.Context, in *payment.PlanItemChange) (*payment.Subscription, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil upgrade request")
	}

	params := &stripesdk.SubscriptionUpdateParams{
		Items: []*stripesdk.SubscriptionUpdateItemParams{
			{
				ID:    stripesdk.String(in.ItemRef),
				Price: stripesdk.String(in.PriceRef),
			},
		},
		ProrationBehavior: stripesdk.String(payment.ProrationAlwaysInvoice.String()),
		PaymentBehavior:   stripesdk.String(paymentBehaviorPendingIfIncomplete),
	}

	if in.ProrationDate != nil && !in.ProrationDate.IsZero() {
		params.ProrationDate = stripesdk.Int64(in.ProrationDate.Unix())
	}

	params.AddExpand("latest_invoice.confirmation_secret")
	params.AddExpand("latest_invoice.payments.data.payment")
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.updateSubscription(ctx, in.SubscriptionRef, params, "stripe subscription upgrade (pending invoice) failed")
}

// CancelAtPeriodEnd schedules cancellation at the end of the current period.
func (p *Provider) CancelAtPeriodEnd(ctx context.Context, in *payment.SubscriptionRef) (*payment.Subscription, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil subscription request")
	}

	params := &stripesdk.SubscriptionUpdateParams{CancelAtPeriodEnd: stripesdk.Bool(true)}
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.updateSubscription(ctx, in.SubscriptionRef, params, "stripe subscription cancel failed")
}

// Resume clears a pending period-end cancellation.
func (p *Provider) Resume(ctx context.Context, in *payment.SubscriptionRef) (*payment.Subscription, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil subscription request")
	}

	params := &stripesdk.SubscriptionUpdateParams{CancelAtPeriodEnd: stripesdk.Bool(false)}
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.updateSubscription(ctx, in.SubscriptionRef, params, "stripe subscription resume failed")
}

// ResetBillingAnchorNow restarts the billing cycle immediately without proration.
func (p *Provider) ResetBillingAnchorNow(ctx context.Context, in *payment.SubscriptionRef) (*payment.Subscription, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil subscription request")
	}

	params := &stripesdk.SubscriptionUpdateParams{
		BillingCycleAnchorNow: stripesdk.Bool(true),
		ProrationBehavior:     stripesdk.String(payment.ProrationNone.String()),
	}
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.updateSubscription(ctx, in.SubscriptionRef, params, "stripe subscription billing-anchor reset failed")
}

// SetDefaultPaymentMethod sets the payment method the subscription bills.
func (p *Provider) SetDefaultPaymentMethod(ctx context.Context, in *payment.DefaultPaymentMethodChange) (*payment.Subscription, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil default payment method request")
	}

	params := &stripesdk.SubscriptionUpdateParams{
		DefaultPaymentMethod: stripesdk.String(in.PaymentMethodRef),
	}
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.updateSubscription(ctx, in.SubscriptionRef, params, "stripe subscription default payment method update failed")
}

// CancelNow cancels a subscription immediately.
func (p *Provider) CancelNow(ctx context.Context, in *payment.SubscriptionRef) (*payment.Subscription, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil subscription request")
	}

	params := &stripesdk.SubscriptionCancelParams{}
	setIdempotency(&params.Params, in.IdempotencyKey)

	canceled, err := p.client.V1Subscriptions.Cancel(ctx, in.SubscriptionRef, params)
	if err != nil {
		return nil, p.fail("stripe subscription cancel now failed", err)
	}

	return p.mappedSubscription(canceled)
}

func (p *Provider) retrieveSubscription(ctx context.Context, ref string, params *stripesdk.SubscriptionRetrieveParams) (*payment.Subscription, *apperr.AppError) {
	live, err := p.client.V1Subscriptions.Retrieve(ctx, ref, params)
	if err != nil {
		return nil, p.fail("stripe subscription retrieve failed", err)
	}

	return p.mappedSubscription(live)
}

func (p *Provider) updateSubscription(ctx context.Context, ref string, params *stripesdk.SubscriptionUpdateParams, logMsg string) (*payment.Subscription, *apperr.AppError) {
	updated, err := p.client.V1Subscriptions.Update(ctx, ref, params)
	if err != nil {
		return nil, p.fail(logMsg, err)
	}

	return p.mappedSubscription(updated)
}

// mappedSubscription treats a success with no object as a provider contract
// break: failing here lets every caller rely on a non-nil result.
func (p *Provider) mappedSubscription(src *stripesdk.Subscription) (*payment.Subscription, *apperr.AppError) {
	if src == nil {
		return nil, payment.ProviderContractBroken("stripe returned no subscription")
	}

	return subscriptionToNeutral(src), nil
}

func subscriptionToNeutral(src *stripesdk.Subscription) *payment.Subscription {
	out := &payment.Subscription{
		Ref:                src.ID,
		CustomerAccountRef: src.CustomerAccount,
		Status:             payment.SubscriptionStatus(src.Status),
		CancelAtPeriodEnd:  src.CancelAtPeriodEnd,
		Metadata:           src.Metadata,
	}

	if src.Customer != nil {
		out.CustomerRef = src.Customer.ID
	}

	if src.Schedule != nil {
		out.ScheduleRef = src.Schedule.ID
	}

	if src.DefaultPaymentMethod != nil {
		out.DefaultPaymentMethodRef = src.DefaultPaymentMethod.ID
	}

	if src.LatestInvoice != nil {
		out.LatestInvoiceRef = src.LatestInvoice.ID
		out.LatestInvoicePaid = src.LatestInvoice.Status == stripesdk.InvoiceStatusPaid
		out.LatestInvoice = invoiceToNeutral(src.LatestInvoice)
	}

	if src.Items == nil {
		return out
	}

	out.Items = make([]payment.SubscriptionItem, 0, len(src.Items.Data))

	for _, item := range src.Items.Data {
		out.Items = append(out.Items, subscriptionItemToNeutral(item))
	}

	return out
}

func subscriptionItemToNeutral(src *stripesdk.SubscriptionItem) payment.SubscriptionItem {
	out := payment.SubscriptionItem{
		Ref:                src.ID,
		CurrentPeriodStart: unixPtrOrNil(src.CurrentPeriodStart),
		CurrentPeriodEnd:   unixPtrOrNil(src.CurrentPeriodEnd),
	}

	if src.Price == nil {
		return out
	}

	out.PriceRef = src.Price.ID
	out.UnitAmount = moneyOf(src.Price.UnitAmount, src.Price.Currency)

	if src.Price.Product != nil {
		out.ProductRef = src.Price.Product.ID
	}

	return out
}
