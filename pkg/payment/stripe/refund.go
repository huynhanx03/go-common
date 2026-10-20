package stripe

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// RefundInFull refunds a payment intent's whole charge. Omitting the amount is
// what makes Stripe refund in full.
func (p *Provider) RefundInFull(ctx context.Context, in *payment.FullRefund) (*payment.Refund, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil refund request")
	}

	params := &stripesdk.RefundCreateParams{PaymentIntent: stripesdk.String(in.PaymentIntentRef)}

	if reason := in.Reason.String(); reason != "" {
		params.Reason = stripesdk.String(reason)
	}

	setIdempotency(&params.Params, in.IdempotencyKey)

	refunded, err := p.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return nil, p.fail("stripe refund create failed", err)
	}

	return refundToNeutral(refunded), nil
}

// RefundAmount refunds a specific amount of a payment intent's charge.
func (p *Provider) RefundAmount(ctx context.Context, in *payment.PartialRefund) (*payment.Refund, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil refund request")
	}

	if in.Amount.Amount <= 0 {
		return nil, payment.Invalid("stripe: partial refund amount must be positive")
	}

	params := &stripesdk.RefundCreateParams{
		PaymentIntent: stripesdk.String(in.PaymentIntentRef),
		Amount:        stripesdk.Int64(in.Amount.Amount),
	}

	if reason := in.Reason.String(); reason != "" {
		params.Reason = stripesdk.String(reason)
	}

	setIdempotency(&params.Params, in.IdempotencyKey)

	refunded, err := p.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return nil, p.fail("stripe partial refund create failed", err)
	}

	return refundToNeutral(refunded), nil
}

func refundToNeutral(src *stripesdk.Refund) *payment.Refund {
	if src == nil {
		return nil
	}

	out := &payment.Refund{
		Ref:           src.ID,
		Status:        payment.RefundStatus(src.Status),
		Amount:        moneyOf(src.Amount, src.Currency),
		FailureReason: string(src.FailureReason),
	}

	if src.PaymentIntent != nil {
		out.PaymentIntentRef = src.PaymentIntent.ID
	}

	return out
}
