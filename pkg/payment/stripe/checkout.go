package stripe

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// OpenSubscription opens an embedded checkout that starts a subscription.
func (p *Provider) OpenSubscription(ctx context.Context, in *payment.SubscriptionCheckout) (*payment.Checkout, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil subscription checkout request")
	}

	params := &stripesdk.CheckoutSessionCreateParams{
		CustomerAccount:   stripesdk.String(in.CustomerAccountRef),
		Mode:              stripesdk.String(string(stripesdk.CheckoutSessionModeSubscription)),
		UIMode:            stripesdk.String(string(stripesdk.CheckoutSessionUIModeEmbeddedPage)),
		ReturnURL:         stripesdk.String(in.ReturnURL),
		LineItems:         catalogLineItems(in.LineItems),
		ClientReferenceID: stripesdk.String(in.ClientReferenceID),
		Metadata:          in.Metadata,
		SubscriptionData:  &stripesdk.CheckoutSessionCreateSubscriptionDataParams{Metadata: in.Metadata},
	}
	setAutomaticTax(params, in.AutomaticTax)
	setDiscounts(params, in.AllowPromotionCodes, in.Discounts)
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.createCheckout(ctx, params, "stripe checkout session create failed")
}

// OpenOneTimePayment opens an embedded checkout for a single ad-hoc payment.
func (p *Provider) OpenOneTimePayment(ctx context.Context, in *payment.OneTimeCheckout) (*payment.Checkout, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil one-time checkout request")
	}

	params := &stripesdk.CheckoutSessionCreateParams{
		CustomerAccount:   stripesdk.String(in.CustomerAccountRef),
		Mode:              stripesdk.String(string(stripesdk.CheckoutSessionModePayment)),
		UIMode:            stripesdk.String(string(stripesdk.CheckoutSessionUIModeEmbeddedPage)),
		ReturnURL:         stripesdk.String(in.ReturnURL),
		LineItems:         inlineLineItems(in.LineItems),
		ClientReferenceID: stripesdk.String(in.ClientReferenceID),
		Metadata:          in.Metadata,
	}
	setAutomaticTax(params, in.AutomaticTax)
	setDiscounts(params, in.AllowPromotionCodes, in.Discounts)
	setIdempotency(&params.Params, in.IdempotencyKey)

	return p.createCheckout(ctx, params, "stripe one-time checkout session create failed")
}

// FetchCheckout retrieves a checkout session.
func (p *Provider) FetchCheckout(ctx context.Context, ref string) (*payment.Checkout, *apperr.AppError) {
	session, err := p.client.V1CheckoutSessions.Retrieve(ctx, ref, nil)
	if err != nil {
		return nil, p.fail("stripe checkout session retrieve failed", err)
	}

	return checkoutToNeutral(session), nil
}

func (p *Provider) createCheckout(ctx context.Context, params *stripesdk.CheckoutSessionCreateParams, logMsg string) (*payment.Checkout, *apperr.AppError) {
	session, err := p.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return nil, p.fail(logMsg, err)
	}

	return checkoutToNeutral(session), nil
}

func setAutomaticTax(params *stripesdk.CheckoutSessionCreateParams, enabled bool) {
	if !enabled {
		return
	}

	params.AutomaticTax = &stripesdk.CheckoutSessionCreateAutomaticTaxParams{
		Enabled: stripesdk.Bool(true),
	}
}

// setDiscounts wires coupons/promotion codes. Stripe treats explicit discounts
// and customer-entered promotion codes as mutually exclusive, so explicit
// discounts win when both are supplied.
func setDiscounts(params *stripesdk.CheckoutSessionCreateParams, allowPromotionCodes bool, discounts []payment.Discount) {
	if len(discounts) > 0 {
		out := make([]*stripesdk.CheckoutSessionCreateDiscountParams, 0, len(discounts))
		for _, d := range discounts {
			discount := &stripesdk.CheckoutSessionCreateDiscountParams{}
			if d.CouponRef != "" {
				discount.Coupon = stripesdk.String(d.CouponRef)
			}
			if d.PromotionCodeRef != "" {
				discount.PromotionCode = stripesdk.String(d.PromotionCodeRef)
			}
			out = append(out, discount)
		}
		params.Discounts = out

		return
	}

	if allowPromotionCodes {
		params.AllowPromotionCodes = stripesdk.Bool(true)
	}
}

func catalogLineItems(items []payment.CheckoutLineItem) []*stripesdk.CheckoutSessionCreateLineItemParams {
	out := make([]*stripesdk.CheckoutSessionCreateLineItemParams, 0, len(items))

	for _, item := range items {
		out = append(out, &stripesdk.CheckoutSessionCreateLineItemParams{
			Price:    stripesdk.String(item.PriceRef),
			Quantity: stripesdk.Int64(quantityOrOne(item.Quantity)),
		})
	}

	return out
}

func inlineLineItems(items []payment.OneTimeLineItem) []*stripesdk.CheckoutSessionCreateLineItemParams {
	out := make([]*stripesdk.CheckoutSessionCreateLineItemParams, 0, len(items))

	for _, item := range items {
		out = append(out, &stripesdk.CheckoutSessionCreateLineItemParams{
			PriceData: &stripesdk.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:    stripesdk.String(item.Amount.Currency),
				UnitAmount:  stripesdk.Int64(item.Amount.Amount),
				ProductData: &stripesdk.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripesdk.String(item.ProductName)},
			},
			Quantity: stripesdk.Int64(quantityOrOne(item.Quantity)),
		})
	}

	return out
}

// quantityOrOne defaults an unset (zero) quantity to one.
func quantityOrOne(q int64) int64 {
	if q <= 0 {
		return 1
	}

	return q
}

func checkoutToNeutral(src *stripesdk.CheckoutSession) *payment.Checkout {
	if src == nil {
		return nil
	}

	out := &payment.Checkout{
		Ref:                src.ID,
		ClientSecret:       src.ClientSecret,
		Status:             payment.CheckoutStatus(src.Status),
		PaymentStatus:      payment.CheckoutPaymentStatus(src.PaymentStatus),
		CustomerAccountRef: src.CustomerAccount,
		ClientReferenceID:  src.ClientReferenceID,
		Metadata:           src.Metadata,
		AmountTotal:        moneyOf(src.AmountTotal, src.Currency),
		AmountTax:          checkoutTax(src),
	}

	if src.Customer != nil {
		out.CustomerRef = src.Customer.ID
	}

	if src.Subscription != nil {
		out.SubscriptionRef = src.Subscription.ID
	}

	if src.Invoice != nil {
		out.InvoiceRef = src.Invoice.ID
	}

	if src.PaymentIntent != nil {
		out.PaymentIntentRef = src.PaymentIntent.ID
	}

	return out
}

func checkoutTax(src *stripesdk.CheckoutSession) payment.Money {
	if src.TotalDetails == nil {
		return payment.NewMoney(0, string(src.Currency))
	}

	return moneyOf(src.TotalDetails.AmountTax, src.Currency)
}
