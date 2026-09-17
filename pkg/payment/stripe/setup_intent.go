package stripe

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// setupIntentUsage stores the method for off-session (later, unattended) charges.
const setupIntentUsage = "off_session"

// CreateSetupIntent sets up a card off-session so it can be charged later.
func (p *Provider) CreateSetupIntent(ctx context.Context, in *payment.NewSetupIntent) (*payment.SetupIntent, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil setup intent request")
	}

	params := &stripesdk.SetupIntentCreateParams{
		CustomerAccount:    stripesdk.String(in.CustomerAccountRef),
		Usage:              stripesdk.String(setupIntentUsage),
		PaymentMethodTypes: []*string{stripesdk.String(string(payment.PaymentMethodTypeCard))},
		Metadata:           in.Metadata,
	}
	setIdempotency(&params.Params, in.IdempotencyKey)

	created, err := p.client.V1SetupIntents.Create(ctx, params)
	if err != nil {
		return nil, p.fail("stripe setup intent create failed", err)
	}

	if created == nil {
		return nil, payment.ProviderContractBroken("stripe returned no setup intent")
	}

	return setupIntentToNeutral(created), nil
}

func setupIntentToNeutral(src *stripesdk.SetupIntent) *payment.SetupIntent {
	out := &payment.SetupIntent{
		Ref:                src.ID,
		CustomerAccountRef: src.CustomerAccount,
		Status:             payment.SetupIntentStatus(src.Status),
		ClientSecret:       src.ClientSecret,
		Metadata:           src.Metadata,
	}

	if src.Customer != nil {
		out.CustomerRef = src.Customer.ID
	}

	if src.PaymentMethod != nil {
		out.PaymentMethodRef = src.PaymentMethod.ID
	}

	return out
}
