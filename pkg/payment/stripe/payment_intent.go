package stripe

import (
	"context"
	"strings"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// FetchPaymentIntent retrieves a payment intent with its latest card details
// expanded.
func (p *Provider) FetchPaymentIntent(ctx context.Context, ref string) (*payment.PaymentIntent, *apperr.AppError) {
	params := &stripesdk.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge.payment_method_details")

	live, err := p.client.V1PaymentIntents.Retrieve(ctx, ref, params)
	if err != nil {
		return nil, p.fail("stripe payment intent retrieve failed", err)
	}

	return paymentIntentToNeutral(live), nil
}

// CreateOneTimeCharge creates an immediate, non-recurring charge.
func (p *Provider) CreateOneTimeCharge(ctx context.Context, in *payment.OneTimeCharge) (*payment.PaymentIntent, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil one-time charge request")
	}

	params := &stripesdk.PaymentIntentCreateParams{
		Amount:   stripesdk.Int64(in.Amount.Amount),
		Currency: stripesdk.String(in.Amount.Currency),
		AutomaticPaymentMethods: &stripesdk.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled:        stripesdk.Bool(true),
			AllowRedirects: stripesdk.String(string(stripesdk.PaymentIntentAutomaticPaymentMethodsAllowRedirectsNever)),
		},
		Metadata: in.Metadata,
	}

	if in.Description != "" {
		params.Description = stripesdk.String(in.Description)
	}

	if in.CustomerAccountRef != "" {
		params.CustomerAccount = stripesdk.String(in.CustomerAccountRef)
	}

	if in.ManualCapture {
		params.CaptureMethod = stripesdk.String(string(stripesdk.PaymentIntentCaptureMethodManual))
	}

	setIdempotency(&params.Params, in.IdempotencyKey)

	created, err := p.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return nil, p.fail("stripe payment intent create (one-time charge) failed", err)
	}

	return paymentIntentToNeutral(created), nil
}

// CapturePayment settles the funds of an authorized payment intent.
func (p *Provider) CapturePayment(ctx context.Context, in *payment.PaymentCapture) (*payment.PaymentIntent, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil capture request")
	}

	params := &stripesdk.PaymentIntentCaptureParams{}

	// A zero amount captures the full authorization; a positive amount captures
	// that much and releases the remainder.
	if in.Amount.Amount > 0 {
		params.AmountToCapture = stripesdk.Int64(in.Amount.Amount)
	}

	setIdempotency(&params.Params, in.IdempotencyKey)

	captured, err := p.client.V1PaymentIntents.Capture(ctx, in.PaymentIntentRef, params)
	if err != nil {
		return nil, p.fail("stripe payment intent capture failed", err)
	}

	return paymentIntentToNeutral(captured), nil
}

// CancelAbandoned cancels a payment intent the customer walked away from.
func (p *Provider) CancelAbandoned(ctx context.Context, in *payment.PaymentIntentRef) (*payment.PaymentIntent, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil payment intent request")
	}

	params := &stripesdk.PaymentIntentCancelParams{
		CancellationReason: stripesdk.String(string(stripesdk.PaymentIntentCancellationReasonAbandoned)),
	}
	setIdempotency(&params.Params, in.IdempotencyKey)

	canceled, err := p.client.V1PaymentIntents.Cancel(ctx, in.PaymentIntentRef, params)
	if err != nil {
		return nil, p.fail("stripe payment intent cancel failed", err)
	}

	return paymentIntentToNeutral(canceled), nil
}

func paymentIntentToNeutral(src *stripesdk.PaymentIntent) *payment.PaymentIntent {
	if src == nil {
		return nil
	}

	out := &payment.PaymentIntent{
		Ref:                src.ID,
		CustomerAccountRef: src.CustomerAccount,
		Status:             payment.PaymentIntentStatus(src.Status),
		Amount:             moneyOf(src.Amount, src.Currency),
		ClientSecret:       strings.TrimSpace(src.ClientSecret),
		Created:            unixPtrOrNil(src.Created),
		Metadata:           src.Metadata,
	}

	if src.Customer != nil {
		out.CustomerRef = src.Customer.ID
	}

	if src.PaymentMethod != nil {
		out.PaymentMethodRef = strings.TrimSpace(src.PaymentMethod.ID)
	}

	if src.LastPaymentError != nil {
		out.LastPaymentError = &payment.PaymentError{
			Code:        string(src.LastPaymentError.Code),
			DeclineCode: string(src.LastPaymentError.DeclineCode),
			Message:     src.LastPaymentError.Msg,
		}
	}

	out.CardBrand, out.CardLast4 = cardFromCharge(src.LatestCharge)

	return out
}

func cardFromCharge(charge *stripesdk.Charge) (brand, last4 string) {
	if charge == nil || charge.PaymentMethodDetails == nil || charge.PaymentMethodDetails.Card == nil {
		return "", ""
	}

	return string(charge.PaymentMethodDetails.Card.Brand), charge.PaymentMethodDetails.Card.Last4
}
