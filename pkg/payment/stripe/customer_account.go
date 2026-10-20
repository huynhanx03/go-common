package stripe

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// CreateCustomerAccount creates a V2 customer account, requesting the automatic
// indirect tax capability so tax can later be computed from the customer's
// location.
//
// It deliberately does not set a customer address. For invoicing and
// subscriptions Stripe Tax picks the customer location as shipping address ->
// billing address -> payment-method billing details -> IP
// (https://docs.stripe.com/tax/customer-locations), but stripe-go v85 types the
// V2 customer shipping address as the legacy AddressParams, which carries only
// `form` struct tags. V2 requests are JSON-encoded (marshalV2JSON), so that type
// serializes with Go field names ("City", "Country", ...) that Stripe does not
// recognize — the address would be silently dropped. Rather than send malformed
// data, we omit it and rely on hosted Checkout, which collects a valid address
// in the session, to drive automatic tax.
func (p *Provider) CreateCustomerAccount(ctx context.Context, in *payment.NewCustomerAccount) (*payment.CustomerAccount, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil customer account request")
	}

	params := &stripesdk.V2CoreAccountCreateParams{
		ContactEmail: stripesdk.String(in.ContactEmail),
		DisplayName:  stripesdk.String(in.DisplayName),
		Configuration: &stripesdk.V2CoreAccountCreateConfigurationParams{
			Customer: &stripesdk.V2CoreAccountCreateConfigurationCustomerParams{
				Capabilities: &stripesdk.V2CoreAccountCreateConfigurationCustomerCapabilitiesParams{
					AutomaticIndirectTax: &stripesdk.V2CoreAccountCreateConfigurationCustomerCapabilitiesAutomaticIndirectTaxParams{
						Requested: stripesdk.Bool(true),
					},
				},
			},
		},
		Metadata: in.Metadata,
	}
	setIdempotency(&params.Params, in.IdempotencyKey)

	account, err := p.client.V2CoreAccounts.Create(ctx, params)
	if err != nil {
		return nil, p.fail("stripe v2 account create failed", err)
	}

	if account == nil {
		return nil, payment.ProviderContractBroken("stripe returned no customer account")
	}

	return &payment.CustomerAccount{Ref: account.ID}, nil
}

// FetchCustomerAccount retrieves a customer account with its default payment
// method resolved.
func (p *Provider) FetchCustomerAccount(ctx context.Context, accountRef string) (*payment.CustomerAccount, *apperr.AppError) {
	params := &stripesdk.V2CoreAccountRetrieveParams{
		Include: []*string{stripesdk.String("configuration.customer")},
	}

	account, err := p.client.V2CoreAccounts.Retrieve(ctx, accountRef, params)
	if err != nil {
		return nil, p.fail("stripe v2 account retrieve failed", err)
	}

	if account == nil {
		return nil, payment.ProviderContractBroken("stripe returned no customer account")
	}

	return &payment.CustomerAccount{
		Ref:                     account.ID,
		DefaultPaymentMethodRef: accountDefaultPaymentMethodRef(account),
	}, nil
}

// SetAccountDefaultPaymentMethod sets the account-level default payment method.
func (p *Provider) SetAccountDefaultPaymentMethod(ctx context.Context, in *payment.CustomerDefaultPaymentMethodChange) (*payment.CustomerAccount, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil default payment method request")
	}

	params := &stripesdk.V2CoreAccountUpdateParams{
		Configuration: &stripesdk.V2CoreAccountUpdateConfigurationParams{
			Customer: &stripesdk.V2CoreAccountUpdateConfigurationCustomerParams{
				Billing: &stripesdk.V2CoreAccountUpdateConfigurationCustomerBillingParams{
					DefaultPaymentMethod: stripesdk.String(in.PaymentMethodRef),
				},
			},
		},
	}
	setIdempotency(&params.Params, in.IdempotencyKey)

	account, err := p.client.V2CoreAccounts.Update(ctx, in.CustomerAccountRef, params)
	if err != nil {
		return nil, p.fail("stripe v2 account default payment method update failed", err)
	}

	if account == nil {
		return nil, payment.ProviderContractBroken("stripe returned no customer account")
	}

	return &payment.CustomerAccount{
		Ref:                     account.ID,
		DefaultPaymentMethodRef: accountDefaultPaymentMethodRef(account),
	}, nil
}

func accountDefaultPaymentMethodRef(account *stripesdk.V2CoreAccount) string {
	if account.Configuration == nil ||
		account.Configuration.Customer == nil ||
		account.Configuration.Customer.Billing == nil {
		return ""
	}

	return account.Configuration.Customer.Billing.DefaultPaymentMethod
}
